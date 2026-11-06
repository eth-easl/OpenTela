package protocol

import (
	"slices"
	"strings"
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

// restoredService builds the advertisement for a service that turned healthy
// again after a withdrawal.
//
// An llm service's model groups are not in the configuration: registerLLMService
// reads them from /v1/models ("model=<id>"), and model-based routing only reaches
// a worker that carries them. If that read fails here, the groups held at
// withdrawal stand in until the next healthy tick reads the list again. As at
// startup, service.identity_group does not apply to an llm service.
func restoredService(name, port string, configured, previous []string) Service {
	if name != "llm" {
		return Service{
			Name:          name,
			Host:          "localhost",
			Port:          port,
			Status:        CONNECTED,
			IdentityGroup: append([]string(nil), configured...),
		}
	}
	models, err := fetchLLMIdentityGroups(port, 1, 0)
	if err != nil {
		if len(previous) == 0 {
			common.Logger.Errorf("Service %s: could not read models on restore (%v) and it held no identity groups; model-based routing will not reach it until a read succeeds", name, err)
		} else {
			common.Logger.Warnf("Service %s: could not read models on restore (%v); re-advertising with the identity groups it held when withdrawn", name, err)
		}
		return newLLMService(port, previous)
	}
	return newLLMService(port, withModelGroups(previous, models))
}

// isModelGroup reports whether a group is one read from /v1/models. The wildcard
// model=* is not: it can only have been set explicitly.
func isModelGroup(group string) bool {
	return strings.HasPrefix(group, "model=") && group != "model=*"
}

// withModelGroups returns groups with its model groups replaced by models. Other
// groups, such as those added through POST /v1/dnt/_node, are kept.
func withModelGroups(groups, models []string) []string {
	kept := make([]string, 0, len(groups))
	for _, g := range groups {
		if !isModelGroup(g) {
			kept = append(kept, g)
		}
	}
	return mergeIdentityGroups(kept, models)
}

// sameGroups reports whether a and b hold the same groups in any order.
func sameGroups(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
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
// remaining perfectly alive to any peer that pings it. Each healthy tick also
// re-reads an llm service's model list, since nothing else keeps it current.
func StartServiceHealthRevalidation() {
	serviceName := viper.GetString("service.name")
	servicePort := viper.GetString("service.port")
	if serviceName == "" || servicePort == "" {
		return
	}
	healthPath := normalizeHealthPath(viper.GetString("service.health_path"))
	interval := serviceHealthInterval()
	threshold := serviceHealthFailureThreshold()

	r := &serviceRevalidator{
		name:       serviceName,
		port:       servicePort,
		key:        serviceKey(Service{Name: serviceName, Host: "localhost", Port: servicePort}),
		threshold:  threshold,
		configured: viper.GetStringSlice("service.identity_group"),
		publish:    ReannounceLocalServices,
	}

	go func() {
		for {
			time.Sleep(interval)

			// One attempt per tick: the interval is the retry cadence, and the
			// threshold is what tolerates blips. Retrying inside a tick would
			// double-count and make the effective threshold unpredictable.
			r.tick(healthCheckRemote(servicePort, healthPath, 1) == nil)
		}
	}()
}

// serviceRevalidator applies health-check results to one service's advertisement.
type serviceRevalidator struct {
	name       string
	port       string
	key        string
	threshold  int
	configured []string
	publish    func()

	state serviceHealthState
	// Identity groups the service carried when it was withdrawn; the fallback
	// if they cannot be read again on restore.
	withdrawnGroups []string
}

func (r *serviceRevalidator) tick(healthy bool) {
	switch r.state.observe(healthy, r.threshold) {
	case serviceHealthWithdraw:
		common.Logger.Warnf("Service %s failed %d consecutive health checks; withdrawing advertisement",
			r.name, r.threshold)
		setServiceWithdrawn(r.key, true)
		r.withdrawnGroups = removeLocalService(r.key)
		r.publish()
	case serviceHealthRestore:
		common.Logger.Infof("Service %s healthy again; re-advertising", r.name)
		// Stay withdrawn until the new entry is in place: building it reads
		// /v1/models, and a reannounce on a peer connect can run meanwhile.
		replaceLocalService(restoredService(r.name, r.port, r.configured, r.withdrawnGroups))
		setServiceWithdrawn(r.key, false)
		r.publish()
	case serviceHealthNoop:
		if healthy {
			r.refreshModelGroups()
		}
	}
}

// refreshModelGroups re-reads an llm service's models and publishes on change.
// The engine behind a port can change without a withdrawal, and an earlier read
// may have failed. On a failed read the current groups are kept.
func (r *serviceRevalidator) refreshModelGroups() {
	if r.name != "llm" {
		return
	}
	models, err := fetchLLMIdentityGroups(r.port, 1, 0)
	if err != nil {
		common.Logger.Warnf("Service %s: could not refresh models: %v", r.name, err)
		return
	}
	if updateLocalModelGroups(r.key, models) {
		common.Logger.Infof("Service %s: models changed; now serving %v", r.name, models)
		r.publish()
	}
}

// removeLocalService drops a service from the advertised set by key and returns
// the identity groups it held, or nil if it was not registered.
func removeLocalService(key string) []string {
	localServicesLock.Lock()
	defer localServicesLock.Unlock()
	var groups []string
	kept := localServices[:0]
	for _, s := range localServices {
		if serviceKey(s) != key {
			kept = append(kept, s)
		} else {
			groups = s.IdentityGroup
		}
	}
	localServices = kept
	return groups
}
