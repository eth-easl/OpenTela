//go:build integration

package integration_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestRelayWatchdogMesh verifies the worker connectivity watchdog on a
// simulated NAT topology.
//
// Two isolated docker bridge networks stand in for the public internet and an
// HPC cluster's private network. Docker drops traffic between user-defined
// bridges, so the head can only reach workers through the relay, exactly like
// a cloud head reaching SLURM workers through the Euler relay:
//
//	pub  (10.213.10.0/24): head, relay
//	priv (10.213.11.0/24): relay, worker-0, worker-1
//
// Scenarios:
//
//  1. Baseline: worker-0 starts normally. It must report "Connectivity OK",
//     advertise the relay as its RelayPeer, and be reachable from the head via
//     the relay hop the gateway uses (/v1/p2p/<relay>/v1/p2p/<worker>/...).
//  2. Startup race: the relay is cut off from priv while worker-1 starts, so
//     its startup bootstrap dial and reservation fail. After the relay comes
//     back, the watchdog must recover worker-1 without a restart.
//  3. Relay restart: the relay container restarts while worker-0 and worker-1
//     stay connected to each other. Before the watchdog, startAutoReconnect
//     did nothing here (the workers still had connections) and recovery was
//     left to the 50%-per-minute Reconnect. The watchdog must notice the lost
//     bootstrap connection and stale reservation and recover both workers.
//
// Run:
//
//	make test TEST_PKGS="./tests/integration/..." \
//	    GOARGS="-tags=integration -count=1 -run TestRelayWatchdogMesh -timeout=15m"
func TestRelayWatchdogMesh(t *testing.T) {
	requireDocker(t)

	srcDir := srcRoot(t)
	buildBinary(t, srcDir)
	buildImage(t, srcDir)
	t.Cleanup(func() { dockerIgnoreErr("rmi", "-f", imageName) })

	for _, n := range []struct{ name, subnet string }{
		{rwPubNet, rwPubSubnet + ".0/24"},
		{rwPrivNet, rwPrivSubnet + ".0/24"},
	} {
		dockerIgnoreErr("network", "rm", n.name)
		dockerMust(t, "network", "create", "--subnet", n.subnet, n.name)
		name := n.name
		t.Cleanup(func() { dockerIgnoreErr("network", "rm", name) })
	}

	relayPubIP, relayPrivIP := rwPubSubnet+".2", rwPrivSubnet+".2"
	headPubIP := rwPubSubnet + ".3"

	// Relay: root of the mesh, on both networks.
	relay := startRWNode(t, "relay", 0, rwPubNet, relayPubIP, []string{
		"start", "--mode", "standalone", "--seed", "11", "--role", "relay",
		"--public-addr", relayPubIP, "--cleanslate=true",
	})
	dockerMust(t, "network", "connect", "--ip", relayPrivIP, rwPrivNet, relay.containerID)
	waitHealthy(t, relay.nodeInfo)
	relayID := selfID(t, relay.nodeInfo)
	t.Logf("relay %s pub=%s priv=%s", relayID, relayPubIP, relayPrivIP)

	// Head: public network only, bootstraps from the relay's public address.
	head := startRWNode(t, "head", 1, rwPubNet, headPubIP, []string{
		"start", "--seed", "12", "--role", "head", "--public-addr", headPubIP,
		"--bootstrap.addr", p2pAddr(relayPubIP, relayID), "--cleanslate=true",
	})
	waitHealthy(t, head.nodeInfo)
	t.Logf("head %s", selfID(t, head.nodeInfo))

	workerArgs := []string{
		"start", "--role", "worker",
		"--bootstrap.addr", p2pAddr(relayPrivIP, relayID), "--cleanslate=true",
	}

	// ---- Scenario 1: baseline ------------------------------------------------
	t.Log("Scenario 1: baseline worker")
	w0 := startRWNode(t, "worker-0", 2, rwPrivNet, rwPrivSubnet+".10", workerArgs)
	waitHealthy(t, w0.nodeInfo)
	w0ID := selfID(t, w0.nodeInfo)
	t.Logf("worker-0 %s", w0ID)
	waitLog(t, w0, "Connectivity OK", 0, 90*time.Second)
	waitRoutable(t, head, relayID, w0, w0ID, 90*time.Second)

	// ---- Scenario 2: startup race -------------------------------------------
	t.Log("Scenario 2: worker starts while the relay is unreachable")
	dockerMust(t, "network", "disconnect", rwPrivNet, relay.containerID)
	w1 := startRWNode(t, "worker-1", 3, rwPrivNet, rwPrivSubnet+".11", workerArgs)
	waitHealthy(t, w1.nodeInfo)
	w1ID := selfID(t, w1.nodeInfo)
	t.Logf("worker-1 %s", w1ID)
	// The watchdog's first check runs 15 s after start.
	waitLog(t, w1, "Connectivity incomplete", 0, 60*time.Second)
	require.NotContains(t, containerAllLogs(w1.nodeInfo), "Connectivity OK",
		"worker-1 cannot be healthy while the relay is unreachable")

	t.Log("Reconnecting relay to the private network")
	dockerMust(t, "network", "connect", "--ip", relayPrivIP, rwPrivNet, relay.containerID)
	waitLog(t, w1, "Connectivity OK", 0, 120*time.Second)
	waitRoutable(t, head, relayID, w1, w1ID, 90*time.Second)
	waitRoutable(t, head, relayID, w0, w0ID, 90*time.Second)

	// ---- Scenario 3: relay restart ------------------------------------------
	t.Log("Scenario 3: relay restarts while workers stay connected to each other")
	logWorkerPeers(t, w0, w1ID)
	okBefore, incompleteBefore := map[string]int{}, map[string]int{}
	for _, w := range []rwNode{w0, w1} {
		logs := containerAllLogs(w.nodeInfo)
		okBefore[w.name] = strings.Count(logs, "Connectivity OK")
		incompleteBefore[w.name] = strings.Count(logs, "Connectivity incomplete")
	}
	dockerMust(t, "restart", relay.containerID)
	waitHealthy(t, relay.nodeInfo)
	require.Equal(t, relayID, selfID(t, relay.nodeInfo), "relay keeps its peer ID (seeded key)")

	for _, w := range []rwNode{w0, w1} {
		// The watchdog must log the outage and then recover.
		waitLog(t, w, "Connectivity incomplete", incompleteBefore[w.name], 90*time.Second)
		waitLog(t, w, "Connectivity OK", okBefore[w.name], 120*time.Second)
	}
	waitRoutable(t, head, relayID, w0, w0ID, 90*time.Second)
	waitRoutable(t, head, relayID, w1, w1ID, 90*time.Second)

	// Workers must not have reserved on each other: relay candidates exclude
	// NATed workers when a bootstrap/relay peer is available.
	for _, w := range []rwNode{w0, w1} {
		other := w1ID
		if w.name == w1.name {
			other = w0ID
		}
		require.NotContains(t, containerAllLogs(w.nodeInfo),
			"Relay reservation on "+other[:12]+" succeeded",
			"%s should not reserve on another worker", w.name)
	}
}

// ---------------------------------------------------------------------------
// Constants and helpers
// ---------------------------------------------------------------------------

const (
	rwPubNet       = "opentela-rw-pub"
	rwPrivNet      = "opentela-rw-priv"
	rwPubSubnet    = "10.213.10"
	rwPrivSubnet   = "10.213.11"
	rwBaseHostPort = baseHostPort + 300
)

type rwNode struct {
	nodeInfo
}

func startRWNode(t *testing.T, name string, index int, netName, ip string, cmd []string) rwNode {
	t.Helper()
	fullName := "opentela-rw-" + name
	hostPort := rwBaseHostPort + index
	dockerIgnoreErr("rm", "-f", fullName)

	args := []string{
		"run", "-d",
		"--name", fullName,
		"--network", netName,
		"--ip", ip,
		"-e", "OF_SECURITY_REQUIRE_SIGNED_BINARY=false",
		"-e", "OF_LOGLEVEL=debug",
		"-p", fmt.Sprintf("127.0.0.1:%d:%s", hostPort, httpPort),
		imageName,
	}
	args = append(args, cmd...)
	id := dockerMust(t, args...)
	n := rwNode{nodeInfo{containerID: id, hostPort: hostPort, name: name}}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("---- %s logs (tail) ----\n%s", name, tailLines(containerAllLogs(n.nodeInfo), 60))
		}
		removeContainer(n.nodeInfo)
	})
	return n
}

func p2pAddr(ip, id string) string {
	return fmt.Sprintf("/ip4/%s/tcp/%s/p2p/%s", ip, p2pPort, id)
}

// selfID reads the node's peer ID from /v1/self, which only answers on
// loopback, so it is queried from inside the container.
func selfID(t *testing.T, n nodeInfo) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		out, err := exec.Command("docker", "exec", n.containerID,
			"wget", "-qO-", "http://127.0.0.1:"+httpPort+"/v1/self").Output()
		if err == nil {
			var self struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(out, &self) == nil && self.ID != "" {
				return self.ID
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("could not read peer ID of %s", n.name)
	return ""
}

// waitLog waits until the container log contains more than `after`
// occurrences of substr.
func waitLog(t *testing.T, n rwNode, substr string, after int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Count(containerAllLogs(n.nodeInfo), substr) > after {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("%s: %q did not appear (more than %d times) within %s", n.name, substr, after, timeout)
}

// waitRoutable waits until the head's node table shows the worker advertising
// the relay as its RelayPeer, and a request from the head reaches the worker
// through the relay hop the gateway uses.
func waitRoutable(t *testing.T, head rwNode, relayID string, w rwNode, wID string, timeout time.Duration) {
	t.Helper()
	client := &http.Client{Timeout: 15 * time.Second}
	hopURL := fmt.Sprintf("http://127.0.0.1:%d/v1/p2p/%s/v1/p2p/%s/v1/health", head.hostPort, relayID, wID)
	var lastRelay, lastErr string
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		lastRelay = advertisedRelay(head, wID)
		if lastRelay == relayID {
			resp, err := client.Get(hopURL)
			if err == nil {
				body, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				// The p2p forward error handler writes "ERROR: ..." with
				// status 200, so the body must be checked too.
				if resp.StatusCode == http.StatusOK && strings.Contains(string(body), `"status":"ok"`) {
					t.Logf("%s routable from head via relay", w.name)
					return
				}
				lastErr = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			} else {
				lastErr = err.Error()
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("%s not routable from head within %s (advertised relay=%q, want %q; last error: %s)",
		w.name, timeout, lastRelay, relayID, lastErr)
}

// advertisedRelay returns the RelayPeer the head's node table holds for id.
func advertisedRelay(head rwNode, id string) string {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/v1/dnt/table", head.hostPort))
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var table map[string]struct {
		ID        string `json:"id"`
		RelayPeer string `json:"relay_peer"`
	}
	if json.NewDecoder(resp.Body).Decode(&table) != nil {
		return ""
	}
	for _, p := range table {
		if p.ID == id {
			return p.RelayPeer
		}
	}
	return ""
}

// logWorkerPeers logs whether w is directly connected to peer `other`. In the
// relay-restart scenario this is what kept the old auto-reconnect loop idle.
func logWorkerPeers(t *testing.T, w rwNode, other string) {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/v1/dnt/peers_status", w.hostPort))
	if err != nil {
		t.Logf("%s peers_status: %v", w.name, err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	t.Logf("%s connected to %s: %v", w.name, other[:12], strings.Contains(string(body), other))
}
