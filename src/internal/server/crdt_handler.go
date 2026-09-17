package server

import (
	"opentela/internal/protocol"
	"strings"
	"time"

	"github.com/axiomhq/axiom-go/axiom"
	"github.com/axiomhq/axiom-go/axiom/ingest"
	"github.com/gin-gonic/gin"
)

func listPeers(c *gin.Context) {
	addrs := protocol.ConnectedPeers()
	c.JSON(200, gin.H{"peers": addrs})
}

func listPeersWithStatus(c *gin.Context) {
	// Get all peers from node table
	peers := protocol.AllPeers()
	c.JSON(200, gin.H{"peers": peers})
}

func listBootstraps(c *gin.Context) {
	addrs := protocol.ConnectedBootstraps()
	c.JSON(200, gin.H{"bootstraps": addrs})
}

func getResourceStats(c *gin.Context) {
	// Call the resource manager stats function from protocol package
	protocol.GetResourceManagerStats()

	// Also return current connection count
	connectedPeers := protocol.ConnectedPeers()
	allPeers := protocol.AllPeers()

	c.JSON(200, gin.H{
		"connected_peers":        len(connectedPeers),
		"total_peers_known":      len(allPeers),
		"connected_peer_details": connectedPeers,
		"all_peer_details":       allPeers,
		"message":                "Resource manager stats logged to console",
	})
}

func updateLocal(c *gin.Context) {
	var peer protocol.Peer
	if err := c.BindJSON(&peer); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	peer.Connected = true
	protocol.UpdateNodeTable(peer)
}

// deleteLocal removes a peer's row from the node table and publishes a
// tombstone so the whole mesh drops it. Localhost only: this is the operator
// escape hatch for ghost rows — dead workers whose eviction never converged.
// Body: {"id": "<peer id>"} ("peer_id" is accepted as an alias).
//
// This handler used to ignore its body entirely and announce this node's
// *own* leave — taking the node itself out of the table instead of the peer
// the operator asked to remove.
func deleteLocal(c *gin.Context) {
	if !isLoopback(c) {
		c.JSON(403, gin.H{"error": "localhost only"})
		return
	}
	var req struct {
		ID     string `json:"id"`
		PeerID string `json:"peer_id"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	peerID := strings.TrimSpace(req.ID)
	if peerID == "" {
		peerID = strings.TrimSpace(req.PeerID)
	}
	if peerID == "" {
		c.JSON(400, gin.H{"error": "peer id required (field \"id\" or \"peer_id\")"})
		return
	}
	if err := protocol.DeletePeerRow(peerID); err != nil {
		if strings.Contains(err.Error(), "peer not found") {
			c.JSON(404, gin.H{"error": err.Error()})
			return
		}
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"deleted": peerID})
}

func getDNT(c *gin.Context) {
	events := []axiom.Event{
		{ingest.TimestampField: time.Now(), "event": "DNT Lookup"},
	}
	IngestEvents(events)
	c.JSON(200, protocol.GetConnectedPeers())
}
