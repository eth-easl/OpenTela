package server

import (
	"ocf/internal/protocol"
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

func deleteLocal(c *gin.Context) {
	var peer protocol.Peer
	if err := c.BindJSON(&peer); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	protocol.AnnounceLeave()
}

func getDNT(c *gin.Context) {
	events := []axiom.Event{
		{ingest.TimestampField: time.Now(), "event": "DNT Lookup"},
	}
	IngestEvents(events)
	c.JSON(200, protocol.GetConnectedPeers())
}
