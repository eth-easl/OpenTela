package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProxyErrorStatus(t *testing.T) {
	assert.Equal(t, http.StatusGatewayTimeout, proxyErrorStatus(context.DeadlineExceeded))
	assert.Equal(t, http.StatusGatewayTimeout,
		proxyErrorStatus(fmt.Errorf("dial: %w", context.DeadlineExceeded)))
	assert.Equal(t, http.StatusBadGateway, proxyErrorStatus(errors.New("failed to dial: no addresses")))
}

func TestErrorHandler_WritesBadGatewayWithMarker(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/p2p/peer/v1/health", nil)

	ErrorHandler(rec, req, errors.New("routing: not found"))

	assert.Equal(t, http.StatusBadGateway, rec.Code, "a failed forward must not report 200")
	assert.Equal(t, "1", rec.Header().Get(proxyErrorHeader))
	assert.Equal(t, "ERROR: routing: not found", rec.Body.String())
}

// End to end through httputil.ReverseProxy: an unreachable upstream must
// surface as a 502 with the proxy error marker.
func TestErrorHandler_ReverseProxyUnreachableUpstream(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close()) // nothing listens here any more

	target, _ := url.Parse("http://" + addr)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = ErrorHandler
	front := httptest.NewServer(proxy)
	defer front.Close()

	resp, err := http.Get(front.URL + "/v1/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	assert.Equal(t, "1", resp.Header.Get(proxyErrorHeader))
	assert.Contains(t, string(body), "ERROR: ")
}

func TestRelayHopError(t *testing.T) {
	marked := &http.Response{StatusCode: http.StatusBadGateway, Header: http.Header{}}
	marked.Header.Set(proxyErrorHeader, "1")
	assert.Error(t, relayHopError(marked), "relay-generated error is a hop failure")

	// A 502 from the worker's own service is passed through, not retried.
	upstream := &http.Response{StatusCode: http.StatusBadGateway, Header: http.Header{}}
	assert.NoError(t, relayHopError(upstream))

	ok := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}}
	assert.NoError(t, relayHopError(ok))
}

// A relay that cannot reach the worker answers with a marked 502. The head's
// relay-hop ModifyResponse must turn that into a retryable transport failure
// instead of committing the 502 to the client; an unmarked 502 from the
// worker's own service is still passed through.
func TestRelayHopError_MakesAttemptRetryable(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/marked" {
			ErrorHandler(w, r, errors.New("failed to dial worker"))
			return
		}
		w.WriteHeader(http.StatusBadGateway) // the worker's own 502
		_, _ = w.Write([]byte("upstream says no"))
	}))
	defer relay.Close()
	target, _ := url.Parse(relay.URL)

	run := func(path string) (*retryableResponseWriter, *httptest.ResponseRecorder) {
		rec := httptest.NewRecorder()
		rw := newRetryableResponseWriter(rec, false, 1<<20)
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.ModifyResponse = relayHopError
		proxy.ErrorHandler = func(res http.ResponseWriter, req *http.Request, err error) {
			if rrw, ok := res.(*retryableResponseWriter); ok {
				rrw.markFailed()
				return
			}
			ErrorHandler(res, req, err)
		}
		proxy.ServeHTTP(rw, httptest.NewRequest(http.MethodGet, path, nil))
		return rw, rec
	}

	rw, rec := run("/marked")
	assert.True(t, rw.isRetryable(), "relay hop failure must be retried on another worker")
	assert.False(t, rw.headersSent, "nothing may reach the client before the retry")
	assert.Equal(t, 0, rec.Body.Len())

	rw, _ = run("/unmarked")
	assert.False(t, rw.isRetryable(), "a worker's own 502 is not a hop failure")
}
