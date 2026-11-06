package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"opentela/internal/common"
	"opentela/internal/platform"

	ds "github.com/ipfs/go-datastore"
	"github.com/spf13/viper"
)

// localServices keeps a thread-safe copy of services this node provides
// so we can re-announce them on reconnects
var (
	localServices     []Service
	localServicesLock = &sync.RWMutex{}
)

// addLocalService appends (deduped) to localServices
func addLocalService(svc Service) {
	localServicesLock.Lock()
	defer localServicesLock.Unlock()
	// simple dedupe on Name|Host|Port
	key := svc.Name + "|" + svc.Host + "|" + svc.Port
	exists := false
	for i := range localServices {
		k := localServices[i].Name + "|" + localServices[i].Host + "|" + localServices[i].Port
		if k == key {
			localServices[i].IdentityGroup = mergeIdentityGroups(localServices[i].IdentityGroup, svc.IdentityGroup)
			exists = true
			break
		}
	}
	if !exists {
		localServices = append(localServices, svc)
	}
}

// replaceLocalService sets svc as the entry for its key. Unlike addLocalService
// it does not merge identity groups, so groups the new entry lacks are dropped.
func replaceLocalService(svc Service) {
	localServicesLock.Lock()
	defer localServicesLock.Unlock()
	key := serviceKey(svc)
	for i := range localServices {
		if serviceKey(localServices[i]) == key {
			localServices[i] = svc
			return
		}
	}
	localServices = append(localServices, svc)
}

// updateLocalModelGroups replaces the model groups of the local service with the
// given key and reports whether its model set changed. It reports false if the
// service is not registered.
func updateLocalModelGroups(key string, models []string) bool {
	localServicesLock.Lock()
	defer localServicesLock.Unlock()
	for i := range localServices {
		if serviceKey(localServices[i]) != key {
			continue
		}
		current := localServices[i].IdentityGroup
		updated := withModelGroups(current, models)
		if sameGroups(current, updated) {
			return false
		}
		localServices[i].IdentityGroup = updated
		return true
	}
	return false
}

// snapshotLocalServices returns a copy of current local services
func snapshotLocalServices() []Service {
	localServicesLock.RLock()
	defer localServicesLock.RUnlock()
	out := make([]Service, len(localServices))
	copy(out, localServices)
	return out
}

// hasLocalServices reports whether any services have been registered locally,
// without copying the slice.
func hasLocalServices() bool {
	localServicesLock.RLock()
	defer localServicesLock.RUnlock()
	return len(localServices) > 0
}

func RegisterLocalServices() {
	serviceName := viper.GetString("service.name")
	servicePort := viper.GetString("service.port")
	healthPath := normalizeHealthPath(viper.GetString("service.health_path"))
	if serviceName == "" || servicePort == "" {
		return
	}
	if serviceName == "llm" {
		// register the service by first fetch available models on the port
		err := healthCheckRemote(servicePort, healthPath, 6000)
		if err != nil {
			common.Logger.Error("could not health check LLM service: ", err)
			return
		}
		common.Logger.Debug("LLM service healthy")
		registerLLMService(servicePort)
		return
	}
	// Generic service: health-check then register with the configured name.
	err := healthCheckRemote(servicePort, healthPath, 6000)
	if err != nil {
		common.Logger.Errorf("could not health check service %s: %v", serviceName, err)
		return
	}
	identityGroup := viper.GetStringSlice("service.identity_group")
	// Same PENDING -> READY flip as the LLM path: the local service passed
	// its health check and is about to be advertised.
	myselfMu.Lock()
	myself.Status = READY
	myselfMu.Unlock()
	RegisterAdHocService(serviceName, servicePort, identityGroup)
}

// RegisterAdHocService injects a synthetic service entry into the local
// CRDT-published service list, bypassing the health-check loop used by
// RegisterLocalServices. Intended for benchmarking/admin use only.
//
// Safe to call before the CRDT store is ready: addLocalService only
// touches in-memory state, and provideService is a no-op if the global
// host/store are nil (returns inside GetCRDTStore -> sync.Once skipped).
// In normal operation the admin route is only mounted after StartServer
// has initialized both, so the CRDT publish path will run.
func RegisterAdHocService(name, port string, identityGroup []string) {
	svc := Service{
		Name:          name,
		Status:        "connected",
		Host:          "localhost",
		Port:          port,
		IdentityGroup: identityGroup,
	}
	provideService(svc)
}

func normalizeHealthPath(healthPath string) string {
	healthPath = strings.TrimSpace(healthPath)
	if healthPath == "" {
		return "/health"
	}
	if !strings.HasPrefix(healthPath, "/") {
		return "/" + healthPath
	}
	return healthPath
}

func healthCheckRemote(port, healthPath string, maxTries int) error {
	const retryInterval = 10 * time.Second
	const logEveryN = 10 // log every 10 retries (~100s)
	start := time.Now()
	healthURL := "http://localhost:" + port + normalizeHealthPath(healthPath)

	for tries := 1; tries <= maxTries; tries++ {
		_, err := common.RemoteGET(healthURL)
		if err == nil {
			elapsed := time.Since(start).Truncate(time.Second)
			common.Logger.Infof("Health check passed after %d/%d attempts (%s elapsed)", tries, maxTries, elapsed)
			return nil
		}

		if tries == 1 || tries%logEveryN == 0 {
			elapsed := time.Since(start).Truncate(time.Second)
			remaining := time.Duration(maxTries-tries) * retryInterval
			common.Logger.Infof("Health check [%d/%d] elapsed %s, ~%s remaining",
				tries, maxTries, elapsed, remaining.Truncate(time.Second))
		}
		time.Sleep(retryInterval)
	}
	return fmt.Errorf("health check failed after %d attempts (%s elapsed)", maxTries, time.Since(start).Truncate(time.Second))
}

// fetchLLMIdentityGroups reads the served model names from the LLM service's
// /v1/models and returns them as "model=<id>" identity groups. It tries up to
// attempts times, sleeping interval between tries, and returns the last error
// if every attempt failed. An empty list counts as a failure: a backend can
// answer 200 with no models while it warms up.
func fetchLLMIdentityGroups(port string, attempts int, interval time.Duration) ([]string, error) {
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		var modelsBytes []byte
		modelsBytes, err = common.RemoteGET("http://localhost:" + port + "/v1/models")
		if err == nil {
			common.Logger.Debug("Fetched models from LLM service: ", string(modelsBytes))
			var availableModels common.LMAvailableModels
			if err = json.Unmarshal(modelsBytes, &availableModels); err == nil {
				if len(availableModels.Models) > 0 {
					var identityGroup []string
					for _, model := range availableModels.Models {
						identityGroup = append(identityGroup, "model="+model.Id)
					}
					return identityGroup, nil
				}
				err = errors.New("LLM service lists no models")
			}
		}
		if attempt < attempts {
			common.Logger.Debugf("could not fetch models from LLM service (attempt %d/%d): %v", attempt, attempts, err)
			time.Sleep(interval)
		}
	}
	return nil, err
}

// newLLMService builds the advertisement for the local llm service.
func newLLMService(port string, identityGroup []string) Service {
	return Service{
		Name:          "llm",
		Status:        CONNECTED,
		Host:          "localhost",
		Port:          port,
		IdentityGroup: identityGroup,
	}
}

func registerLLMService(port string) {
	// The service just passed its health check, but /v1/models can still fail
	// transiently (e.g. the server finishing warm-up). A blip here registers
	// this node without model identity groups until the health revalidation
	// loop reads them — retry briefly, and be loud on final failure since
	// model-based routing will not reach this node until then.
	const modelFetchAttempts = 10
	const modelFetchInterval = 3 * time.Second
	identityGroup, err := fetchLLMIdentityGroups(port, modelFetchAttempts, modelFetchInterval)
	if err != nil {
		common.Logger.Errorf("could not fetch models from LLM service after %d attempts, registering without model identity groups: %v", modelFetchAttempts, err)
	}

	// register the models
	service := newLLMService(port, identityGroup)
	// Flip peer-level Status to ready now that healthCheckRemote has passed
	// and we're about to advertise the model.
	myselfMu.Lock()
	myself.Status = READY
	myselfMu.Unlock()
	provideService(service)
}

func provideService(service Service) {
	host, _ := GetP2PNode(nil)
	ctx := context.Background()
	store, _ := GetCRDTStore()
	key := ds.NewKey(host.ID().String())
	// track locally and publish full set (deduped)
	addLocalService(service)
	myselfMu.Lock()
	myself.Service = snapshotLocalServices()
	if viper.GetString("public-addr") != "" {
		myself.PublicAddress = viper.GetString("public-addr")
	}
	applyLocalRuntimeMetadata(&myself)
	common.Logger.Debug("Registering LLM service: ", myself)
	value, err := json.Marshal(myself)
	myselfMu.Unlock()
	UpdateNodeTableHook(key, value)
	common.ReportError(err, "Error while marshalling peer")
	err = store.Put(ctx, key, value)
	if err != nil {
		common.Logger.Debug("Error while providing service: ", err)
	}
}

// ResetLocalServicesForTest is intended for tests that need a clean slate.
// It is exported only so that test files in other packages can call it.
func ResetLocalServicesForTest() {
	localServicesLock.Lock()
	defer localServicesLock.Unlock()
	localServices = nil
}

// SetLocalServicesForTest replaces the in-memory local service set. Test-only.
func SetLocalServicesForTest(services []Service) {
	localServicesLock.Lock()
	defer localServicesLock.Unlock()
	localServices = append([]Service(nil), services...)
}

// ReannounceLocalServices re-publishes this node's service entry, used after reconnects.
// It merges services from the in-memory localServices list with any services already
// in the node table (e.g. those registered via the HTTP API) to avoid overwriting them.
func ReannounceLocalServices() {
	host, _ := GetP2PNode(nil)
	ctx := context.Background()
	store, _ := GetCRDTStore()
	key := ds.NewKey(host.ID().String())
	// refresh hardware and services
	gpus := platform.GetGPUInfo()
	// Start from localServices (the authoritative in-memory set) and merge
	// any extra services present in the current node table entry so that
	// services registered through other paths (e.g. HTTP API) are not lost.
	var existingServices []Service
	if existing, err := GetPeerFromTable(host.ID().String()); err == nil {
		existingServices = existing.Service
	}
	// The withdrawn set must be honoured here: without it this merge re-adds a
	// service that the health revalidation loop just retired, and the withdrawal
	// silently no-ops on the very next reannounce.
	merged := mergeAdvertisedServices(snapshotLocalServices(), existingServices, snapshotWithdrawnServices())
	myselfMu.Lock()
	myself.Hardware.GPUs = gpus
	myself.Service = merged
	if viper.GetString("public-addr") != "" {
		myself.PublicAddress = viper.GetString("public-addr")
	}
	applyLocalRuntimeMetadata(&myself)
	value, err := json.Marshal(myself)
	myselfMu.Unlock()
	if err != nil {
		common.Logger.Debug("Error marshalling self during reannounce: ", err)
		return
	}
	UpdateNodeTableHook(key, value)
	if err := store.Put(ctx, key, value); err != nil {
		common.Logger.Debug("Failed to reannounce local services: ", err)
	} else {
		common.Logger.Debug("Re-announced local services")
	}
}
