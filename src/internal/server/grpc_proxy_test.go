package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func mockP2PForwardHandler(c *gin.Context, targetURL *url.URL) {
	director := func(req *http.Request) {
		req.URL.Scheme = targetURL.Scheme
		req.URL.Path = targetURL.Path + c.Param("path")
		req.URL.Host = req.Host
		req.Host = targetURL.Host
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	proxy.Director = director
	
	if strings.HasPrefix(c.Request.Header.Get("Content-Type"), "application/grpc") {
		proxy.Transport = getLocalH2CTransport()
	} else {
		proxy.Transport = http.DefaultTransport
	}

	proxy.ServeHTTP(c.Writer, c.Request)
}

func TestGRPCProxyRouting(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstreamEngine := gin.Default()
	upstreamEngine.POST("/proxy", func(c *gin.Context) {
		require.Equal(t, "application/grpc", c.Request.Header.Get("Content-Type"))
		require.Equal(t, "HTTP/2.0", c.Request.Proto)
		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		require.Equal(t, []byte("mock-grpc-request"), body)
		
		c.Header("Content-Type", "application/grpc")
		c.String(http.StatusOK, "mock-grpc-response")
	})

	h2cHandler := h2c.NewHandler(upstreamEngine, &http2.Server{})
	upstreamServer := httptest.NewUnstartedServer(h2cHandler)
	upstreamServer.Start()
	defer upstreamServer.Close()

	proxyEngine := gin.Default()
	proxyEngine.Any("/*path", func(c *gin.Context) {
		target, _ := url.Parse(upstreamServer.URL)
		mockP2PForwardHandler(c, target)
	})

	proxyServer := httptest.NewUnstartedServer(h2c.NewHandler(proxyEngine, &http2.Server{}))
	proxyServer.Start()
	defer proxyServer.Close()

	clientTransport := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	}
	client := &http.Client{Transport: clientTransport}

	// Important: use https to trigger DialTLSContext (which actually dials plain TCP)
	proxyURL := strings.Replace(proxyServer.URL, "http://", "https://", 1) + "/proxy"
	req, err := http.NewRequest("POST", proxyURL, bytes.NewBuffer([]byte("mock-grpc-request")))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/grpc")

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/grpc", resp.Header.Get("Content-Type"))
	
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "mock-grpc-response", string(respBody))
}

func TestGlobalH2CTransport(t *testing.T) {
	// Initialize myself so GetP2PNode doesn't nil panic if it relies on package state
	// (depends on protocol init, assuming it's safe to call or transport does it lazily)
	transport := getGlobalH2CTransport()
	require.NotNil(t, transport)
	require.True(t, transport.AllowHTTP)

	// Test with invalid peer ID
	_, err := transport.DialTLSContext(context.Background(), "tcp", "invalid-peer-id:8080", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to decode peer ID")

	// Test with a valid peer ID (gostream.Dial will likely fail to connect since no actual node exists locally or peer is unreachable, but we test the decode part passes)
	validPeerID := "12D3KooWNLAigNKKAmFQSeBCFpAYHpkDKFg8CQhL2B5H2fJrEog2"
	_, err = transport.DialTLSContext(context.Background(), "tcp", validPeerID+":8080", nil)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "failed to decode peer ID")
}

