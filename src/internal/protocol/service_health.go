package protocol

import (
	"sync"
	"time"

	"opentela/internal/common"

	"github.com/spf13/viper"
)

// Defaults for the revalidation loop. With these, a backing engine that dies
// is withdrawn ~90s later. The threshold exists because an HTTP timeout carries
// no authority: unlike a relay's NO_RESERVATION, a single failed health check
// is a poor reason to drop a service that may just be busy.
const (
	defaultServiceHealthInterval = 30 * time.Second
	defaultServiceHealthFailures = 3
)

type serviceHealthAction int

const (
	serviceHealthNoop serviceHealthAction = iota
	serviceHealthWithdraw
	serviceHealthRestore
)

func (a serviceHealthAction) String() string {
	switch a {
	case serviceHealthWithdraw:
		return "withdraw"
	case serviceHealthRestore:
		return "restore"
	default:
		return "noop"
	}
}

// serviceHealthState tracks consecutive health-check outcomes for one service
// and decides when its advertisement should change.
type serviceHealthState struct {
	failures  int
	withdrawn bool
}

// observe records one health-check result and reports what should happen to the
// advertisement. It returns an action only on a transition, so callers can
// publish on change rather than on every tick.
func (s *serviceHealthState) observe(healthy bool, threshold int) serviceHealthAction {
	if healthy {
		s.failures = 0
		if s.withdrawn {
			s.withdrawn = false
			return serviceHealthRestore
		}
		return serviceHealthNoop
	}

	s.failures++
	if s.withdrawn || s.failures < threshold {
		return serviceHealthNoop
	}
	s.withdrawn = true
	return serviceHealthWithdraw
}

// serviceKey is the identity used for dedupe and withdrawal bookkeeping. It
// matches addLocalService's key so the two cannot drift apart.
func serviceKey(s Service) string {
	return s.Name + "|" + s.Host + "|" + s.Port
}

// mergeAdvertisedServices combines the authoritative local set with services
// discovered in this node's existing table entry, skipping anything currently
// withdrawn.
//
// The withdrawn filter is the point. Without it, the merge re-adds a service we
// just retired for failing its health check, and the withdrawal silently
// no-ops on the very next reannounce.
func mergeAdvertisedServices(local, existing []Service, withdrawn map[string]bool) []Service {
	merged := make([]Service, 0, len(local)+len(existing))
	seen := make(map[string]struct{}, len(local))

	for _, s := range local {
		key := serviceKey(s)
		if withdrawn[key] {
			continue
		}
		seen[key] = struct{}{}
		merged = append(merged, s)
	}

	for _, s := range existing {
		key := serviceKey(s)
		if withdrawn[key] {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		merged = append(merged, s)
	}
	return merged
}

// Withdrawal bookkeeping, consulted by mergeAdvertisedServices.
var (
	withdrawnServices     = map[string]bool{}
	withdrawnServicesLock = &sync.RWMutex{}
)

func snapshotWithdrawnServices() map[string]bool {
	withdrawnServicesLock.RLock()
	defer withdrawnServicesLock.RUnlock()
	out := make(map[string]bool, len(withdrawnServices))
	for k, v := range withdrawnServices {
		out[k] = v
	}
	return out
}

func setServiceWithdrawn(key string, withdrawn bool) {
	withdrawnServicesLock.Lock()
	defer withdrawnServicesLock.Unlock()
	if withdrawn {
		withdrawnServices[key] = true
		return
	}
	delete(withdrawnServices, key)
}

// localServiceIdentityGroups returns a copy of the identity groups of the local
// service with the given key, or nil if it is not registered.
func localServiceIdentityGroups(key string) []string {
	for _, s := range snapshotLocalServices() {
		if serviceKey(s) == key {
			return append([]string(nil), s.IdentityGroup...)
		}
	}
	return nil
}

// restoredService builds the advertisement for a service that turned healthy
// again after a withdrawal.
//
// An llm service's identity groups are not in the configuration: at startup
// registerLLMService reads them from /v1/models ("model=<id>"), and model-based
// routing only reaches a worker that carries them. Restoring with the
// configured groups alone brings the worker back ready but unroutable, and
// nothing repairs it short of a restart. So the model list is read again; it
// just passed a health check, so one attempt is enough. If that read fails, the
// groups the service had when it was withdrawn are used instead.
func restoredService(name, port string, configured, previous []string) Service {
	groups := append([]string(nil), configured...)
	if name == "llm" {
		fetched, err := llmIdentityGroupsFetcher(port, 1, 0)
		if err != nil || len(fetched) == 0 {
			common.Logger.Warnf("Service %s: could not re-read models on restore (%v); re-advertising with its previous identity groups", name, err)
			fetched = previous
		}
		groups = mergeIdentityGroups(groups, fetched)
	}
	return Service{
		Name:          name,
		Host:          "localhost",
		Port:          port,
		Status:        CONNECTED,
		IdentityGroup: groups,
	}
}

// mergeIdentityGroups appends the groups in extra that base lacks, keeping order.
func mergeIdentityGroups(base, extra []string) []string {
	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, list := range [][]string{base, extra} {
		for _, g := range list {
			if _, ok := seen[g]; ok {
				continue
			}
			seen[g] = struct{}{}
			out = append(out, g)
		}
	}
	return out
}

func serviceHealthInterval() time.Duration {
	if d := viper.GetDuration("service.health_interval"); d > 0 {
		return d
	}
	return defaultServiceHealthInterval
}

func serviceHealthFailureThreshold() int {
	if n := viper.GetInt("service.health_failures"); n > 0 {
		return n
	}
	return defaultServiceHealthFailures
}

// StartServiceHealthRevalidation re-checks this node's registered service on an
// interval and withdraws its advertisement when the backing engine stops
// answering.
//
// RegisterLocalServices only health-checks once, at startup. That leaves the
// case where otela outlives its engine — the normal shape of a SLURM job ending,
// since they are separate processes — advertising a model it cannot serve while
// remaining perfectly alive to any peer that pings it.
func StartServiceHealthRevalidation() {
	serviceName := viper.GetString("service.name")
	servicePort := viper.GetString("service.port")
	if serviceName == "" || servicePort == "" {
		return
	}
	healthPath := normalizeHealthPath(viper.GetString("service.health_path"))
	interval := serviceHealthInterval()
	threshold := serviceHealthFailureThreshold()

	go func() {
		state := &serviceHealthState{}
		key := serviceKey(Service{Name: serviceName, Host: "localhost", Port: servicePort})
		// Identity groups the service carried when it was withdrawn; the
		// fallback if they cannot be read again on restore.
		var withdrawnGroups []string

		for {
			time.Sleep(interval)

			// One attempt per tick: the interval is the retry cadence, and the
			// threshold is what tolerates blips. Retrying inside a tick would
			// double-count and make the effective threshold unpredictable.
			healthy := healthCheckRemote(servicePort, healthPath, 1) == nil

			switch state.observe(healthy, threshold) {
			case serviceHealthWithdraw:
				common.Logger.Warnf("Service %s failed %d consecutive health checks; withdrawing advertisement",
					serviceName, threshold)
				withdrawnGroups = localServiceIdentityGroups(key)
				setServiceWithdrawn(key, true)
				removeLocalService(key)
				ReannounceLocalServices()
			case serviceHealthRestore:
				common.Logger.Infof("Service %s healthy again; re-advertising", serviceName)
				setServiceWithdrawn(key, false)
				addLocalService(restoredService(serviceName, servicePort,
					viper.GetStringSlice("service.identity_group"), withdrawnGroups))
				ReannounceLocalServices()
			case serviceHealthNoop:
			}
		}
	}()
}

// removeLocalService drops a service from the advertised set by key.
func removeLocalService(key string) {
	localServicesLock.Lock()
	defer localServicesLock.Unlock()
	kept := localServices[:0]
	for _, s := range localServices {
		if serviceKey(s) != key {
			kept = append(kept, s)
		}
	}
	localServices = kept
}
