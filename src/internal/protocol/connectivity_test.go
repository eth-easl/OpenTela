package protocol

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestConnectivityWatchdog_RetriesUntilBootstrapAndRelayHold(t *testing.T) {
	bs, rr := false, false
	var reconnects, reserves int
	w := &connectivityWatchdog{
		bootstrapConnected: func() bool { return bs },
		relayReserved:      func() bool { return rr },
		reconnect:          func(context.Context) { reconnects++ },
		reserve:            func() { reserves++ },
	}
	ctx := context.Background()

	// Isolated: both retried, short backoff (never the 30s healthy interval).
	d := w.step(ctx)
	assert.Equal(t, 1, reconnects)
	assert.Equal(t, 1, reserves)
	assert.Less(t, d, 30*time.Second)

	// Bootstrap back but no reservation: only the reservation is retried.
	bs = true
	w.step(ctx)
	assert.Equal(t, 1, reconnects)
	assert.Equal(t, 2, reserves)

	// Fully connected: no side effects, relaxed interval, backoff reset.
	rr = true
	assert.Equal(t, 30*time.Second, w.step(ctx))
	assert.Equal(t, 1, reconnects)
	assert.Equal(t, 2, reserves)
	assert.Equal(t, 0, w.attempt)

	// Reservation lapses (expiry) -> retried again.
	rr = false
	w.step(ctx)
	assert.Equal(t, 3, reserves)
}

func TestHasRelayReservation(t *testing.T) {
	relayReservation.mu.Lock()
	relayReservation.expires = time.Time{}
	relayReservation.mu.Unlock()
	assert.False(t, HasRelayReservation())

	setRelayReservation(time.Now().Add(time.Hour))
	assert.True(t, HasRelayReservation())

	// Within the safety margin of expiry counts as not reserved.
	relayReservation.mu.Lock()
	relayReservation.expires = time.Now().Add(30 * time.Second)
	relayReservation.mu.Unlock()
	assert.False(t, HasRelayReservation())
}
