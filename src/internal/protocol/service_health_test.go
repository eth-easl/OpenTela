package protocol

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"opentela/internal/common"
)

// A service is health-checked once at startup and then advertised forever. If
// the backing engine dies while otela survives — the normal shape of a SLURM
// job ending, since otela and sglang are separate processes — the node stays
// genuinely alive and pingable while advertising a model it can no longer
// serve. No head-side liveness probe can detect that by construction, so the
// producer has to notice it itself.
func TestServiceHealthState_Observe(t *testing.T) {
	const threshold = 3

	t.Run("healthy service is left advertised", func(t *testing.T) {
		s := &serviceHealthState{}
		if got := s.observe(true, threshold); got != serviceHealthNoop {
			t.Fatalf("observe(healthy) = %v, want %v", got, serviceHealthNoop)
		}
	})

	t.Run("failures below the threshold do not withdraw", func(t *testing.T) {
		s := &serviceHealthState{}
		for i := 1; i < threshold; i++ {
			if got := s.observe(false, threshold); got != serviceHealthNoop {
				t.Fatalf("failure %d: observe(unhealthy) = %v, want %v — a single blip must not drop a busy service", i, got, serviceHealthNoop)
			}
		}
	})

	t.Run("reaching the threshold withdraws once", func(t *testing.T) {
		s := &serviceHealthState{}
		for i := 1; i < threshold; i++ {
			s.observe(false, threshold)
		}
		if got := s.observe(false, threshold); got != serviceHealthWithdraw {
			t.Fatalf("observe at threshold = %v, want %v", got, serviceHealthWithdraw)
		}
		// Already withdrawn: further failures must not re-publish the same fact.
		if got := s.observe(false, threshold); got != serviceHealthNoop {
			t.Fatalf("observe past threshold = %v, want %v", got, serviceHealthNoop)
		}
	})

	t.Run("recovery restores the service", func(t *testing.T) {
		s := &serviceHealthState{}
		for i := 0; i < threshold; i++ {
			s.observe(false, threshold)
		}
		if got := s.observe(true, threshold); got != serviceHealthRestore {
			t.Fatalf("observe(healthy) after withdrawal = %v, want %v", got, serviceHealthRestore)
		}
		// And the failure count must have reset, or the next single failure
		// would immediately re-withdraw a service that just proved healthy.
		if got := s.observe(false, threshold); got != serviceHealthNoop {
			t.Fatalf("first failure after recovery = %v, want %v — failure count did not reset", got, serviceHealthNoop)
		}
	})
}

// ReannounceLocalServices merges services found in the node-table entry back
// into the advertised set, so that services registered through other paths are
// not lost. Left alone, that merge would immediately resurrect a service we
// just withdrew for being unhealthy, and the withdrawal would silently no-op.
func TestMergeAdvertisedServices(t *testing.T) {
	local := []Service{{Name: "llm", Port: "30000"}}
	existing := []Service{
		{Name: "llm", Port: "30000"},
		{Name: "flash-sandbox", Port: "8080"},
	}

	t.Run("services from the table are merged in", func(t *testing.T) {
		got := mergeAdvertisedServices(local, existing, nil)
		if len(got) != 2 {
			t.Fatalf("merged = %+v, want both llm and flash-sandbox", got)
		}
	})

	t.Run("a withdrawn service is not resurrected by the merge", func(t *testing.T) {
		withdrawn := map[string]bool{serviceKey(Service{Name: "flash-sandbox", Port: "8080"}): true}
		got := mergeAdvertisedServices(local, existing, withdrawn)
		for _, s := range got {
			if s.Name == "flash-sandbox" {
				t.Fatalf("merged = %+v, want flash-sandbox to stay withdrawn", got)
			}
		}
		if len(got) != 1 {
			t.Fatalf("merged = %+v, want only llm", got)
		}
	})
}

// fakeLLM is a real HTTP backend, so these tests exercise the real model fetch.
type fakeLLM struct {
	mu        sync.Mutex
	models    []string
	failNext  int
	onModels  func()
	modelHits atomic.Int64
	port      string
}

func newFakeLLM(t *testing.T, models ...string) *fakeLLM {
	t.Helper()
	f := &fakeLLM{models: models}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.modelHits.Add(1)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.onModels != nil {
			f.onModels()
		}
		if f.failNext > 0 {
			f.failNext--
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		data := make([]common.ModelData, 0, len(f.models))
		for _, m := range f.models {
			data = append(data, common.ModelData{Id: m})
		}
		_ = json.NewEncoder(w).Encode(common.LMAvailableModels{Object: "list", Models: data})
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, f.port, err = net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fakeLLM) serve(failNext int, models ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failNext = failNext
	f.models = models
}

// revalidatorFixture registers an llm service with the given groups and counts
// publishes.
type revalidatorFixture struct {
	*serviceRevalidator
	backend   *fakeLLM
	publishes int
}

func newRevalidatorFixture(t *testing.T, groups []string, configured []string, models ...string) *revalidatorFixture {
	t.Helper()
	ResetLocalServicesForTest()
	f := newFakeLLM(t, models...)
	fx := &revalidatorFixture{backend: f}
	fx.serviceRevalidator = &serviceRevalidator{
		name:       "llm",
		port:       f.port,
		key:        serviceKey(Service{Name: "llm", Host: "localhost", Port: f.port}),
		threshold:  2,
		configured: configured,
		publish:    func() { fx.publishes++ },
	}
	t.Cleanup(func() {
		ResetLocalServicesForTest()
		setServiceWithdrawn(fx.key, false)
	})
	addLocalService(Service{Name: "llm", Host: "localhost", Port: f.port, Status: CONNECTED, IdentityGroup: groups})
	return fx
}

func (fx *revalidatorFixture) withdraw(t *testing.T) {
	t.Helper()
	fx.tick(false)
	fx.tick(false)
	if _, ok := fx.advertised(); ok {
		t.Fatalf("service still advertised after %d failures", fx.threshold)
	}
}

// advertised returns what a reannounce would publish for the service right now.
func (fx *revalidatorFixture) advertised() ([]string, bool) {
	for _, s := range mergeAdvertisedServices(snapshotLocalServices(), nil, snapshotWithdrawnServices()) {
		if serviceKey(s) == fx.key {
			return s.IdentityGroup, true
		}
	}
	return nil, false
}

func (fx *revalidatorFixture) wantGroups(t *testing.T, want ...string) {
	t.Helper()
	got, ok := fx.advertised()
	if !ok {
		t.Fatalf("service not advertised, want groups %v", want)
	}
	if len(got) != len(want) || (len(want) > 0 && !reflect.DeepEqual(got, want)) {
		t.Fatalf("identity groups = %v, want %v", got, want)
	}
}

// The incident this guards against: a withdrawn llm worker came back ready with
// no model groups, so model-based routing never reached it again.
func TestRevalidator_RestoreRereadsModels(t *testing.T) {
	fx := newRevalidatorFixture(t, []string{"model=A"}, nil, "A")
	fx.withdraw(t)
	fx.backend.serve(0, "B")
	fx.tick(true)
	fx.wantGroups(t, "model=B")
}

// /v1/models can fail just after a passed health check. A new job may also have
// changed the model, so the fallback must not stand once a read succeeds.
func TestRevalidator_FailedRestoreReadIsCorrectedOnNextTick(t *testing.T) {
	fx := newRevalidatorFixture(t, []string{"model=A"}, nil, "A")
	fx.withdraw(t)
	fx.backend.serve(1, "B")

	fx.tick(true)
	fx.wantGroups(t, "model=A") // the groups held at withdrawal

	before := fx.publishes
	fx.tick(true)
	fx.wantGroups(t, "model=B")
	if fx.publishes != before+1 {
		t.Fatalf("publishes = %d, want the corrected groups published once", fx.publishes-before)
	}
}

func TestRevalidator_NoGroupsAfterFailedStartupAndRestoreIsRepaired(t *testing.T) {
	fx := newRevalidatorFixture(t, nil, nil, "A")
	fx.withdraw(t)
	fx.backend.serve(1, "A")

	fx.tick(true)
	fx.wantGroups(t)

	fx.tick(true)
	fx.wantGroups(t, "model=A")
}

func TestRevalidator_HealthyTickPicksUpModelChange(t *testing.T) {
	fx := newRevalidatorFixture(t, []string{"model=A"}, nil, "B")
	fx.tick(true)
	fx.wantGroups(t, "model=B")
	if fx.publishes != 1 {
		t.Fatalf("publishes = %d, want 1", fx.publishes)
	}
}

func TestRevalidator_HealthyTickPublishesOnlyOnChange(t *testing.T) {
	fx := newRevalidatorFixture(t, []string{"model=A", "model=B"}, nil, "A", "B")
	fx.tick(true)
	fx.backend.serve(0, "B", "A") // same set, different order
	fx.tick(true)
	if fx.publishes != 0 {
		t.Fatalf("publishes = %d, want none for an unchanged model set", fx.publishes)
	}
}

func TestRevalidator_HealthyTickKeepsGroupsWhenReadFails(t *testing.T) {
	fx := newRevalidatorFixture(t, []string{"model=A"}, nil, "A")

	fx.backend.serve(1, "A")
	fx.tick(true)
	fx.wantGroups(t, "model=A")

	fx.backend.serve(0) // 200 with an empty list, as during warm-up
	fx.tick(true)
	fx.wantGroups(t, "model=A")

	if fx.publishes != 0 {
		t.Fatalf("publishes = %d, want none when nothing changed", fx.publishes)
	}
}

// Groups added through POST /v1/dnt/_node are not ours to remove. Only groups
// read from /v1/models are replaced.
func TestRevalidator_RefreshKeepsOtherGroups(t *testing.T) {
	fx := newRevalidatorFixture(t, []string{"team=a", "model=A", "model=*"}, nil, "B")
	fx.tick(true)
	fx.wantGroups(t, "team=a", "model=*", "model=B")
}

func TestRevalidator_UnhealthyTickDoesNotReadModels(t *testing.T) {
	fx := newRevalidatorFixture(t, []string{"model=A"}, nil, "A")
	fx.tick(false)
	if hits := fx.backend.modelHits.Load(); hits != 0 {
		t.Fatalf("/v1/models read %d times on a failing tick", hits)
	}
}

// registerLLMService ignores service.identity_group, so honouring it on restore
// would change a worker's routing identity after a flap.
func TestRevalidator_RestoreDoesNotAddConfiguredGroupsToLLM(t *testing.T) {
	fx := newRevalidatorFixture(t, []string{"model=A"}, []string{"all"}, "A")
	fx.withdraw(t)
	fx.tick(true)
	fx.wantGroups(t, "model=A")
}

// POST /v1/dnt/_node reaches addLocalService without consulting the withdrawn
// set, so the old row can come back while the service is withdrawn.
func TestRevalidator_RestoreDropsModelsReaddedWhileWithdrawn(t *testing.T) {
	fx := newRevalidatorFixture(t, []string{"model=A"}, nil, "A")
	fx.withdraw(t)
	addLocalService(Service{Name: "llm", Host: "localhost", Port: fx.port, IdentityGroup: []string{"model=A"}})
	fx.backend.serve(0, "B")
	fx.tick(true)
	fx.wantGroups(t, "model=B")
}

// A reannounce on a peer connect can run during the /v1/models read. The service
// must stay withdrawn until its new groups are in place.
func TestRevalidator_ServiceStaysWithdrawnDuringRestoreRead(t *testing.T) {
	fx := newRevalidatorFixture(t, []string{"model=A"}, nil, "A")
	fx.withdraw(t)

	oldRow := []Service{{Name: "llm", Host: "localhost", Port: fx.port, IdentityGroup: []string{"model=A"}}}
	var duringRead []Service
	fx.backend.mu.Lock()
	fx.backend.models = []string{"B"}
	fx.backend.onModels = func() {
		duringRead = mergeAdvertisedServices(snapshotLocalServices(), oldRow, snapshotWithdrawnServices())
	}
	fx.backend.mu.Unlock()

	fx.tick(true)
	if len(duringRead) != 0 {
		t.Fatalf("a reannounce during the read would publish %+v", duringRead)
	}
	fx.wantGroups(t, "model=B")
}

func TestRevalidator_NonLLMKeepsConfiguredGroupsAndIsNotProbed(t *testing.T) {
	ResetLocalServicesForTest()
	f := newFakeLLM(t, "A")
	key := serviceKey(Service{Name: "flash-sandbox", Host: "localhost", Port: f.port})
	t.Cleanup(func() {
		ResetLocalServicesForTest()
		setServiceWithdrawn(key, false)
	})
	addLocalService(Service{Name: "flash-sandbox", Host: "localhost", Port: f.port, IdentityGroup: []string{"team=a"}})
	r := &serviceRevalidator{
		name: "flash-sandbox", port: f.port, key: key, threshold: 1,
		configured: []string{"team=a"}, publish: func() {},
	}

	r.tick(true)
	r.tick(false)
	r.tick(true)

	services := snapshotLocalServices()
	if len(services) != 1 || !reflect.DeepEqual(services[0].IdentityGroup, []string{"team=a"}) {
		t.Fatalf("services = %+v, want flash-sandbox with [team=a]", services)
	}
	if hits := f.modelHits.Load(); hits != 0 {
		t.Fatalf("/v1/models read %d times for a non-llm service", hits)
	}
}
