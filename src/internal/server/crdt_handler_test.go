package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func init() {
	viper.Set("seed", "123456789")
	viper.Set("tcpport", "0") // 0 means random port
	viper.Set("udpport", "0")
	viper.Set("public-addr", "127.0.0.1")
}

func setupRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.Default()
	// Map the handlers to routes similar to how server.go would (assuming simple mapping for test)
	// Functions in crdt_handler.go are:
	// listPeers, listPeersWithStatus, listBootstraps, getResourceStats,
	// updateLocal, deleteLocal, getDNT (this one seems less standard?)

	r.GET("/peers", listPeers)
	r.GET("/peers/status", listPeersWithStatus)
	r.GET("/bootstraps", listBootstraps)
	r.GET("/resources", getResourceStats)
	// r.POST("/local", updateLocal) // Need body
	// r.DELETE("/local", deleteLocal) // Need body
	// r.GET("/dnt", getDNT)

	return r
}

func TestListPeers(t *testing.T) {
	// Setup generic node table state
	// We can't easily inject into protocol package from here (server package)
	// unless we use exported functions.
	// protocol.UpdateNodeTable updates the CRDT.
	// But that requires a running CRDT store/host which might be heavy.

	// Issue: server package tests depend on protocol package state.
	// Ideally we'd mock protocol functions, but they are direct function calls in handlers.
	// e.g. protocol.ConnectedPeers()

	// If we can't mock, we must rely on protocol's global state (dangerous but common in legacy/simple Go apps).
	// Or we just test the handler wiring if getting state is too hard.

	// Let's try to set up a minimal valid state if possible.
	// protocol.UpdateNodeTable requires P2PNode and CRDTStore.
	// Initializing those might be complex (requires libp2p host etc).

	// Alternative: Verify the handler calls the function.
	// But without mocking, it calls the real function which returns empty/nil if not initialized.
	// protocol.ConnectedPeers() returns map (safe).

	r := setupRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/peers", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.Nil(t, err)
	assert.Contains(t, response, "peers")
}

func TestListPeersWithStatus(t *testing.T) {
	r := setupRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/peers/status", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.Nil(t, err)
	assert.Contains(t, response, "peers")
}

func TestGetResourceStats(t *testing.T) {
	r := setupRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/resources", nil)
	r.ServeHTTP(w, req)

	// It calls protocol.GetResourceManagerStats() which might log to console.
	// And returns JSON.

	assert.Equal(t, 200, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.Nil(t, err)
	assert.Contains(t, response, "connected_peers")
	assert.Contains(t, response, "total_peers_known")
}
