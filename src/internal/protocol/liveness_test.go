package protocol

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/libp2p/go-libp2p/core/network"
	pbv2 "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/pb"
	"github.com/spf13/viper"
)

// classifyProbeError matches a string because circuitv2/client.relayError is
// unexported — there is no type to assert on and no sentinel for errors.Is.
// This test is what makes that safe: it rebuilds the error from go-libp2p's own
// Status_name table using the same format string as client.dial, so a rename or
// reformat upstream fails here instead of silently degrading every probe to
// probeUnknown (at which point ghosts would never be evicted again).
func TestClassifyProbeError_MatchesRealLibp2pStatusName(t *testing.T) {
	status := pbv2.Status_NO_RESERVATION
	// Mirrors client/dial.go:177:
	//   newRelayError("error opening relay circuit: %s (%d)", pbv2.Status_name[int32(status)], status)
	realErr := fmt.Errorf("error opening relay circuit: %s (%d)",
		pbv2.Status_name[int32(status)], status)

	if got := classifyProbeError(realErr); got != probeDeadAuthoritative {
		t.Fatalf("classifyProbeError(%q) = %v, want %v — relayNoReservation (%q) no longer matches what go-libp2p emits",
			realErr, got, probeDeadAuthoritative, relayNoReservation)
	}
}

// The whole point of the liveness probe is that a failed dial is not by itself
// evidence that the *worker* is gone — it is usually evidence about the relay.
// Only the relay's NO_RESERVATION answer is a statement about the worker, so
// only that may evict. Everything else must fail safe (leave the peer alone),
// because evicting on an ambiguous failure would wipe every worker behind a
// relay that happens to be restarting.
func TestClassifyProbeError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want probeVerdict
	}{
		{
			name: "no error means the peer answered a ping",
			err:  nil,
			want: probeAlive,
		},
		{
			name: "relay reports no reservation: the worker is definitively detached",
			err:  errors.New("error opening relay circuit: NO_RESERVATION (204)"),
			want: probeDeadAuthoritative,
		},
		{
			name: "wrapped no-reservation is still authoritative",
			err:  fmt.Errorf("dial %s: %w", "QmAbc", errors.New("error opening relay circuit: NO_RESERVATION (204)")),
			want: probeDeadAuthoritative,
		},
		{
			name: "deadline exceeded says nothing about the worker",
			err:  context.DeadlineExceeded,
			want: probeUnknown,
		},
		{
			name: "relay unreachable says nothing about the worker",
			err:  errors.New("dial tcp 10.128.1.1:43917: connect: connection refused"),
			want: probeUnknown,
		},
		{
			name: "relay refusing us says nothing about the worker",
			err:  errors.New("error opening relay circuit: PERMISSION_DENIED (202)"),
			want: probeUnknown,
		},
		{
			name: "relay resource limits say nothing about the worker",
			err:  errors.New("error opening relay circuit: RESOURCE_LIMIT_EXCEEDED (201)"),
			want: probeUnknown,
		},
		{
			// The relay holds a reservation but could not open the stream. That
			// is a claim about reachability, not attachment, and it can be
			// transient at the relay — so it must not evict.
			name: "connection failed at the relay is not authoritative",
			err:  errors.New("error opening relay circuit: CONNECTION_FAILED (203)"),
			want: probeUnknown,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyProbeError(tc.err); got != tc.want {
				t.Fatalf("classifyProbeError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// selfRelayVerdict is the branch that fixes ghosts relayed by the node itself:
// when the dispatcher and the relay are one process (a node can never be
// libp2p-connected to its own peer ID), the dial path below could never
// produce a verdict and workers behind it were un-evictable. The local
// connectedness must mirror the relay's reservation semantics: connected
// means alive, anything else (NotConnected, Limited, CannotConnect) is the
// same statement NO_RESERVATION would make.
func TestSelfRelayVerdict(t *testing.T) {
	cases := []struct {
		name          string
		connectedness network.Connectedness
		want          probeVerdict
	}{
		{"connected worker is alive", network.Connected, probeAlive},
		{"no connection means no reservation: dead", network.NotConnected, probeDeadAuthoritative},
		{"circuit-only connection lost the reservation too", network.Limited, probeDeadAuthoritative},
		{"unknown connectedness fails safe as absent", network.Connectedness(99), probeDeadAuthoritative},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := selfRelayVerdict(tc.connectedness); got != tc.want {
				t.Fatalf("selfRelayVerdict(%v) = %v, want %v", tc.connectedness, got, tc.want)
			}
		})
	}
}

// mayPublishEviction must let a node publish when it is itself the peer's
// advertised relay — the dispatcher-in-one-process deployment — even though its
// role is "worker". Anything else non-head must stay silent.
func TestMayPublishEviction(t *testing.T) {
	oldRole, oldMyID := viper.GetString("role"), MyID
	defer func() {
		viper.Set("role", oldRole)
		MyID = oldMyID
	}()

	viper.Set("role", "head")
	if !mayPublishEviction(Peer{ID: "w", RelayPeer: "some-other-relay"}) {
		t.Fatal("a head must be allowed to publish any proven eviction")
	}

	viper.Set("role", "worker")
	MyID = "relay-self"
	if !mayPublishEviction(Peer{ID: "w", RelayPeer: "relay-self"}) {
		t.Fatal("a node that is the peer's advertised relay must be allowed to publish")
	}
	if mayPublishEviction(Peer{ID: "w", RelayPeer: "another-relay"}) {
		t.Fatal("a worker that is not the peer's relay must not publish")
	}
	if mayPublishEviction(Peer{ID: "w"}) {
		t.Fatal("a worker must not publish when the peer advertises no relay")
	}
}
