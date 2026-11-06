package protocol

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeHealthPath(t *testing.T) {
	tests := map[string]string{
		"":         "/health",
		"   ":      "/health",
		"status":   "/status",
		" /ready ": "/ready",
	}
	for input, expected := range tests {
		t.Run(input, func(t *testing.T) {
			assert.Equal(t, expected, normalizeHealthPath(input))
		})
	}
}

func TestHealthCheckRemote_CustomPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	serverURL, err := url.Parse(server.URL)
	assert.NoError(t, err)
	_, port, err := net.SplitHostPort(serverURL.Host)
	assert.NoError(t, err)
	assert.NoError(t, healthCheckRemote(port, "/status", 1))
}

func TestRegisterAdHocService_AddsToLocalServices(t *testing.T) {
	// Reset package state so test is isolated.
	localServicesLock.Lock()
	localServices = nil
	localServicesLock.Unlock()

	// RegisterAdHocService calls provideService which requires a live libp2p
	// host (os.Exit(1) if none). We test the in-memory half (addLocalService)
	// directly, using the same Service shape that RegisterAdHocService builds.
	// The full CRDT-publish path is exercised by the integration test in Task 4.
	svc := Service{
		Name:          "convbench-test",
		Status:        "connected",
		Host:          "localhost",
		Port:          "65000",
		IdentityGroup: nil,
	}
	addLocalService(svc)

	svcs := snapshotLocalServices()
	assert.Len(t, svcs, 1)
	assert.Equal(t, "convbench-test", svcs[0].Name)
	assert.Equal(t, "65000", svcs[0].Port)
	assert.Equal(t, "localhost", svcs[0].Host)
	assert.Equal(t, "connected", svcs[0].Status)
}

func TestLocalServiceSnapshot(t *testing.T) {
	// start with empty registry
	localServices = nil
	addLocalService(Service{Name: "llm", Host: "localhost", Port: "8000", IdentityGroup: []string{"model=a"}})
	addLocalService(Service{Name: "llm", Host: "localhost", Port: "8000", IdentityGroup: []string{"model=b"}})

	snap := snapshotLocalServices()
	if len(snap) != 1 {
		t.Fatalf("expected 1 service after dedupe, got %d", len(snap))
	}
	if len(snap[0].IdentityGroup) != 2 {
		t.Fatalf("expected merged identity groups, got %v", snap[0].IdentityGroup)
	}
}

func TestFetchLLMIdentityGroups(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"Kimi-K3.1-Pro"},{"id":"Kimi-K3.1-Pro-alias"}]}`))
	}))
	serverURL, err := url.Parse(server.URL)
	assert.NoError(t, err)
	_, port, err := net.SplitHostPort(serverURL.Host)
	assert.NoError(t, err)

	groups, err := fetchLLMIdentityGroups(port, 1, 0)
	assert.NoError(t, err)
	assert.Equal(t, []string{"model=Kimi-K3.1-Pro", "model=Kimi-K3.1-Pro-alias"}, groups)

	server.Close()
	groups, err = fetchLLMIdentityGroups(port, 2, 0)
	assert.Error(t, err)
	assert.Nil(t, groups)
}
