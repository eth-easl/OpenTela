package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
)

// Custom recorder that implements CloseNotify
type TestResponseRecorder struct {
	*httptest.ResponseRecorder
	closeNotifyChan chan bool
}

func (r *TestResponseRecorder) CloseNotify() <-chan bool {
	return r.closeNotifyChan
}

func NewTestResponseRecorder() *TestResponseRecorder {
	return &TestResponseRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		closeNotifyChan:  make(chan bool, 1),
	}
}

// Mock upstream server
func newMockUpstream() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body) // Drain body
		w.WriteHeader(http.StatusOK)
	}))
}

// Old implementation (simulated)
func oldProxyHandler(c *gin.Context, target *url.URL) {
	// Simulate reading all body
	body, _ := io.ReadAll(c.Request.Body)
	// Simulate creating new transport
	tr := &http.Transport{
		DisableKeepAlives: true,
	}
	// Simplified proxy logic just for benchmark comparison
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = tr
	// Director that uses the buffered body
	proxy.Director = func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.URL.Path = target.Path
		req.Body = io.NopCloser(bytes.NewBuffer(body))
	}
	proxy.ServeHTTP(c.Writer, c.Request)
}

// New implementation (simulated)
func newProxyHandler(c *gin.Context, target *url.URL) {
	// Simplified proxy logic
	proxy := httputil.NewSingleHostReverseProxy(target)
	// Uses DefaultTransport (pooled)
	proxy.Director = func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.URL.Path = target.Path
		// Direct streaming - no body read
	}
	proxy.ServeHTTP(c.Writer, c.Request)
}

func BenchmarkProxyRequest_1MB(b *testing.B) {
	gin.SetMode(gin.TestMode)
	upstream := newMockUpstream()
	defer upstream.Close()
	targetURL, _ := url.Parse(upstream.URL)

	// Pre-allocate payload
	payload := make([]byte, 1024*1024)

	b.ResetTimer()
	b.Run("Old_Buffering_NoPool", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			w := NewTestResponseRecorder()
			c, _ := gin.CreateTestContext(w)
			// Reset body for each request
			c.Request = httptest.NewRequest("POST", "/test", bytes.NewReader(payload))
			oldProxyHandler(c, targetURL)
		}
	})

	b.Run("New_Streaming_Pooled", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			w := NewTestResponseRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/test", bytes.NewReader(payload))
			newProxyHandler(c, targetURL)
		}
	})
}
