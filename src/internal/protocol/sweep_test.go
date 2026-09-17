package protocol

import (
	"testing"
	"time"
)

// The sweep used to skip every peer carrying a service ("they may be behind a
// relay"), which made LLM workers — the only peers that ever carry a service —
// permanently un-evictable. The replacement is evidence-based: a service peer
// may be marked disconnected, but only when the relay authoritatively says it
// is gone. Ambiguous probe failures must still leave it alone, or a restarting
// relay would evict every worker behind it at once.
func TestDecideSweepAction(t *testing.T) {
	now := time.Unix(100000, 0)
	fresh := now.Add(-10 * time.Second).Unix()
	stale := now.Add(-30 * time.Minute).Unix()

	withSvc := []Service{{Name: "llm"}}

	cases := []struct {
		name    string
		peer    Peer
		verdict probeVerdict
		want    peerSweepAction
	}{
		{
			name:    "service peer confirmed gone by relay is disconnected",
			peer:    Peer{ID: "a", Connected: true, LastSeen: stale, Service: withSvc},
			verdict: probeDeadAuthoritative,
			want:    sweepMarkDisconnected,
		},
		{
			name:    "service peer with unreachable relay is left alone",
			peer:    Peer{ID: "b", Connected: true, LastSeen: stale, Service: withSvc},
			verdict: probeUnknown,
			want:    sweepKeep,
		},
		{
			name:    "service peer that answered is left alone",
			peer:    Peer{ID: "c", Connected: true, LastSeen: fresh, Service: withSvc},
			verdict: probeAlive,
			want:    sweepKeep,
		},
		{
			name:    "plain peer silent past the disconnect window is disconnected",
			peer:    Peer{ID: "d", Connected: true, LastSeen: stale},
			verdict: probeUnknown,
			want:    sweepMarkDisconnected,
		},
		{
			name:    "plain peer seen recently is left alone",
			peer:    Peer{ID: "e", Connected: true, LastSeen: fresh},
			verdict: probeUnknown,
			want:    sweepKeep,
		},
		{
			name:    "plain peer disconnected past the stale window is deleted",
			peer:    Peer{ID: "f", Connected: false, LastSeen: stale},
			verdict: probeUnknown,
			want:    sweepDelete,
		},
		{
			// Deliberate: the entry stays visible for debugging once
			// disconnected. Only routing consults Connected, so a kept row is
			// inert, and operators lose the trail if we delete it.
			name:    "disconnected service peer is kept for inspection, not deleted",
			peer:    Peer{ID: "g", Connected: false, LastSeen: stale, Service: withSvc},
			verdict: probeUnknown,
			want:    sweepKeep,
		},
		{
			// ...but the trail is capped: a row disconnected for more than
			// peerServiceStaleAfter is not coming back (a live worker
			// re-registers within minutes) and only accumulates — one ghost
			// per ended job, forever, otherwise.
			name:    "disconnected service peer past the retention cap is deleted",
			peer:    Peer{ID: "g2", Connected: false, LastSeen: now.Add(-8 * 24 * time.Hour).Unix(), Service: withSvc},
			verdict: probeUnknown,
			want:    sweepDelete,
		},
		{
			name:    "disconnected service peer just inside the retention cap is kept",
			peer:    Peer{ID: "g3", Connected: false, LastSeen: now.Add(-6 * 24 * time.Hour).Unix(), Service: withSvc},
			verdict: probeUnknown,
			want:    sweepKeep,
		},
		{
			name:    "peer with no LastSeen is left alone",
			peer:    Peer{ID: "h", Connected: true, LastSeen: 0},
			verdict: probeUnknown,
			want:    sweepKeep,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := decideSweepAction(tc.peer, tc.verdict, now); got != tc.want {
				t.Fatalf("decideSweepAction(%+v, %v) = %v, want %v", tc.peer, tc.verdict, got, tc.want)
			}
		})
	}
}
