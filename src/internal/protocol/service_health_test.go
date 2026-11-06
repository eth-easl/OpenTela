package protocol

import (
	"errors"
	"reflect"
	"testing"
	"time"
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

// A withdrawn llm service used to come back with only the configured identity
// groups. Its model groups are read from /v1/models at startup, not from the
// configuration, so the restored worker was ready but unroutable by model.
func TestRestoredService(t *testing.T) {
	stubFetcher := func(t *testing.T, groups []string, err error) {
		t.Helper()
		original := llmIdentityGroupsFetcher
		llmIdentityGroupsFetcher = func(string, int, time.Duration) ([]string, error) { return groups, err }
		t.Cleanup(func() { llmIdentityGroupsFetcher = original })
	}

	t.Run("llm restore re-reads the model groups", func(t *testing.T) {
		stubFetcher(t, []string{"model=Kimi-K3.1-Pro"}, nil)
		got := restoredService("llm", "19218", nil, []string{"model=stale"})
		if !reflect.DeepEqual(got.IdentityGroup, []string{"model=Kimi-K3.1-Pro"}) {
			t.Fatalf("identity groups = %v, want the fetched model groups", got.IdentityGroup)
		}
		if got.Status != CONNECTED || got.Port != "19218" || got.Host != "localhost" {
			t.Fatalf("service = %+v, want a connected localhost service on 19218", got)
		}
	})

	t.Run("configured groups are kept alongside the model groups", func(t *testing.T) {
		stubFetcher(t, []string{"model=m", "all"}, nil)
		got := restoredService("llm", "19218", []string{"all"}, nil)
		if !reflect.DeepEqual(got.IdentityGroup, []string{"all", "model=m"}) {
			t.Fatalf("identity groups = %v, want [all model=m]", got.IdentityGroup)
		}
	})

	t.Run("a failed re-read falls back to the groups held at withdrawal", func(t *testing.T) {
		stubFetcher(t, nil, errors.New("connection refused"))
		got := restoredService("llm", "19218", nil, []string{"model=Kimi-K3.1-Pro"})
		if !reflect.DeepEqual(got.IdentityGroup, []string{"model=Kimi-K3.1-Pro"}) {
			t.Fatalf("identity groups = %v, want the groups held at withdrawal", got.IdentityGroup)
		}
	})

	t.Run("a non-llm service keeps its configured groups and is not probed", func(t *testing.T) {
		original := llmIdentityGroupsFetcher
		llmIdentityGroupsFetcher = func(string, int, time.Duration) ([]string, error) {
			t.Fatal("fetcher called for a non-llm service")
			return nil, nil
		}
		t.Cleanup(func() { llmIdentityGroupsFetcher = original })
		got := restoredService("flash-sandbox", "8080", []string{"team=a"}, []string{"model=x"})
		if !reflect.DeepEqual(got.IdentityGroup, []string{"team=a"}) {
			t.Fatalf("identity groups = %v, want [team=a]", got.IdentityGroup)
		}
	})
}

func TestLocalServiceIdentityGroups(t *testing.T) {
	ResetLocalServicesForTest()
	t.Cleanup(ResetLocalServicesForTest)
	addLocalService(Service{Name: "llm", Host: "localhost", Port: "19218", IdentityGroup: []string{"model=m"}})
	key := serviceKey(Service{Name: "llm", Host: "localhost", Port: "19218"})
	got := localServiceIdentityGroups(key)
	if !reflect.DeepEqual(got, []string{"model=m"}) {
		t.Fatalf("groups = %v, want [model=m]", got)
	}
	got[0] = "mutated"
	if again := localServiceIdentityGroups(key); again[0] != "model=m" {
		t.Fatalf("returned slice aliases the local service: %v", again)
	}
	if missing := localServiceIdentityGroups("none|localhost|1"); missing != nil {
		t.Fatalf("groups for an unknown service = %v, want nil", missing)
	}
}
