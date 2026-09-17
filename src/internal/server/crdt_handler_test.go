package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	ds "github.com/ipfs/go-datastore"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"

	"opentela/internal/protocol"
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

func TestDeleteLocal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.DELETE("/v1/dnt/_node", deleteLocal)

	// Seed a ghost row directly in the protocol table.
	ghost := protocol.Peer{ID: "ghost-worker", Connected: true, LastSeen: 5000}
	b, _ := json.Marshal(ghost)
	protocol.UpdateNodeTableHook(ds.NewKey("ghost-worker"), b)

	doDelete := func(remoteAddr, body string) *httptest.ResponseRecorder {
		req, _ := http.NewRequest("DELETE", "/v1/dnt/_node", strings.NewReader(body))
		req.RemoteAddr = remoteAddr
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// Remote callers are refused: this is the operator ghost-purge hatch.
	if w := doDelete("10.1.2.3:4444", `{"id":"ghost-worker"}`); w.Code != 403 {
		t.Fatalf("remote delete: got %d, want 403 (%s)", w.Code, w.Body.String())
	}

	// Missing peer id.
	if w := doDelete("127.0.0.1:9999", `{}`); w.Code != 400 {
		t.Fatalf("missing id: got %d, want 400", w.Code)
	}

	// Unknown peer.
	if w := doDelete("127.0.0.1:9999", `{"id":"never-seen"}`); w.Code != 404 {
		t.Fatalf("unknown peer: got %d, want 404 (%s)", w.Code, w.Body.String())
	}

	// Happy path: the ghost row is gone and the node's own row untouched.
	//
	// The old implementation ignored the body and announced this node's OWN
	// leave — if that behavior ever comes back, this assertion fails because
	// DeletePeerRow refuses to delete self.
	if w := doDelete("127.0.0.1:9999", `{"id":"ghost-worker"}`); w.Code != 200 {
		t.Fatalf("localhost delete: got %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if _, err := protocol.GetPeerFromTable("ghost-worker"); err == nil {
		t.Fatal("ghost row must be removed from the table")
	}
}
