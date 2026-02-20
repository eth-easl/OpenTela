package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"ocf/internal/common"
	"ocf/internal/protocol"
	"strings"
	"sync"
	"time"

	"github.com/axiomhq/axiom-go/axiom"
	"github.com/axiomhq/axiom-go/axiom/ingest"
	"github.com/buger/jsonparser"
	"github.com/gin-gonic/gin"
	gostream "github.com/libp2p/go-libp2p-gostream"
	p2phttp "github.com/libp2p/go-libp2p-http"
	"github.com/libp2p/go-libp2p/core/peer"
	"golang.org/x/net/http2"
)

var (
	globalTransport     *http.Transport
	globalH2CTransport  *http2.Transport
	localH2CTransport   *http2.Transport
	transportOnce       sync.Once
	h2cTransportOnce    sync.Once
	localH2COnce        sync.Once
)

func getGlobalH2CTransport() *http2.Transport {
	h2cTransportOnce.Do(func() {
		node, _ := protocol.GetP2PNode(nil)
		globalH2CTransport = &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
				host, _, err := net.SplitHostPort(addr)
				if err != nil {
					host = addr
				}
				serverPID, err := peer.Decode(host)
				if err != nil {
					return nil, fmt.Errorf("failed to decode peer ID: %w", err)
				}
				return gostream.Dial(ctx, node, serverPID, p2phttp.DefaultP2PProtocol)
			},
		}
	})
	return globalH2CTransport
}

func getLocalH2CTransport() *http2.Transport {
	localH2COnce.Do(func() {
		localH2CTransport = &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, addr)
			},
		}
	})
	return localH2CTransport
}

func getGlobalTransport() *http.Transport {
	transportOnce.Do(func() {
		node, _ := protocol.GetP2PNode(nil)
		globalTransport = &http.Transport{
			ResponseHeaderTimeout: 10 * time.Minute, // Allow up to 10 minutes for response headers
			IdleConnTimeout:       90 * time.Second, // Keep connections alive for 90 seconds
			DisableKeepAlives:     false,            // Enable keep-alives for better performance
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
		}
		globalTransport.RegisterProtocol("libp2p", p2phttp.NewTransport(node))
	})
	return globalTransport
}

func ErrorHandler(res http.ResponseWriter, req *http.Request, err error) {
	if _, werr := fmt.Fprintf(res, "ERROR: %s", err.Error()); werr != nil {
		common.Logger.Error("Error writing error response: ", werr)
	}
}

// StreamAwareResponseWriter wraps the response writer to handle streaming
type StreamAwareResponseWriter struct {
	http.ResponseWriter
	flusher http.Flusher
}

func (s *StreamAwareResponseWriter) WriteHeader(statusCode int) {
	// Enable streaming headers if this is a streaming response
	if s.ResponseWriter.Header().Get("Content-Type") == "text/event-stream" {
		s.ResponseWriter.Header().Set("Cache-Control", "no-cache")
		s.ResponseWriter.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering
	}
	s.ResponseWriter.WriteHeader(statusCode)
}

func (s *StreamAwareResponseWriter) Flush() {
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

// Unwrap for Go 1.20+ ResponseController compatibility
func (s *StreamAwareResponseWriter) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}

// Hijack implements http.Hijacker
func (s *StreamAwareResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := s.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// Push implements http.Pusher
func (s *StreamAwareResponseWriter) Push(target string, opts *http.PushOptions) error {
	if p, ok := s.ResponseWriter.(http.Pusher); ok {
		return p.Push(target, opts)
	}
	return http.ErrNotSupported
}

// P2P handler for forwarding requests to other peers
func P2PForwardHandler(c *gin.Context) {
	// Set a longer timeout for AI/ML services
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Minute)
	defer cancel()
	// Pass the context to the request
	c.Request = c.Request.WithContext(ctx)

	requestPeer := c.Param("peerId")
	requestPath := c.Param("path")

	// Log event as before
	event := []axiom.Event{{ingest.TimestampField: time.Now(), "event": "P2P Forward", "from": &protocol.MyID, "to": requestPeer, "path": requestPath}}
	IngestEvents(event)

	target := url.URL{
		Scheme: "libp2p",
		Host:   requestPeer,
		Path:   requestPath,
	}
	common.Logger.Infof("Forwarding request to %s", target.String())

	director := func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Path = target.Path
		req.URL.Host = req.Host
		req.Host = target.Host
		// DO NOT read body here; httputil.ReverseProxy will stream it from c.Request.Body
	}

	proxy := httputil.NewSingleHostReverseProxy(&target)
	proxy.Director = director
	if strings.HasPrefix(c.Request.Header.Get("Content-Type"), "application/grpc") {
		proxy.Transport = getGlobalH2CTransport()
	} else {
		proxy.Transport = getGlobalTransport()
	}
	proxy.ErrorHandler = ErrorHandler
	proxy.ModifyResponse = rewriteHeader()

	// Wrap the response writer to handle streaming properly
	streamWriter := &StreamAwareResponseWriter{
		ResponseWriter: c.Writer,
		flusher:        c.Writer.(http.Flusher),
	}
	proxy.ServeHTTP(streamWriter, c.Request)
}

// ServiceHandler
func ServiceForwardHandler(c *gin.Context) {
	serviceName := c.Param("service")
	requestPath := c.Param("path")
	service, err := protocol.GetService(serviceName)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	target := url.URL{
		Scheme: "http",
		Host:   service.Host + ":" + service.Port,
		Path:   requestPath,
	}
	director := func(req *http.Request) {
		req.Host = target.Host
		req.URL.Host = req.Host
		req.URL.Scheme = target.Scheme
		req.URL.Path = target.Path
	}
	proxy := httputil.NewSingleHostReverseProxy(&target)
	proxy.Director = director
	if strings.HasPrefix(c.Request.Header.Get("Content-Type"), "application/grpc") {
		proxy.Transport = getLocalH2CTransport()
	} else {
		proxy.Transport = getGlobalTransport()
	}

	streamWriter := &StreamAwareResponseWriter{
		ResponseWriter: c.Writer,
		flusher:        c.Writer.(http.Flusher),
	}
	proxy.ServeHTTP(streamWriter, c.Request)
}

// in case of global service, we need to forward the request to the service, identified by the service name and identity group
func GlobalServiceForwardHandler(c *gin.Context) {
	// Set a longer timeout for AI/ML services
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Minute)
	defer cancel()

	// Create a copy of the request body to preserve it for streaming
	// We MUST read body here to inspect IdentityGroup
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	c.Request = c.Request.WithContext(ctx)

	serviceName := c.Param("service")
	requestPath := c.Param("path")
	providers, err := protocol.GetAllProviders(serviceName)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Use the already read bodyBytes instead of reading again
	body := bodyBytes
	// find proper service that are within the same identity group
	// first filter by service name, then iterative over the identity groups
	// always find all the services that are in the same identity group
	var candidates []string
	for _, provider := range providers {
		for _, service := range provider.Service {
			if service.Name == serviceName {
				var selected = false
				// check if the service is in the same identity group
				if len(service.IdentityGroup) > 0 {
					for _, ig := range service.IdentityGroup {
						igGroup := strings.Split(ig, "=")
						igKey := igGroup[0]
						igValue := igGroup[1]
						requestGroup, err := jsonparser.GetString(body, igKey)
						if err == nil && requestGroup == igValue {
							selected = true
							break
						}
					}
				}
				// append the service to the candidates
				if selected {
					candidates = append(candidates, provider.ID)
				}
			}
		}
	}
	if len(candidates) < 1 {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "No provider found for the requested service."})
		return
	}

	// randomly select one of the candidates
	// here's where we can implement a load balancing algorithm
	randomIndex := rand.Intn(len(candidates))

	// Re-construct body for forwarding since we read it
	// (Already done above: c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)))

	targetPeer := candidates[randomIndex]
	// replace the request path with the _service path
	requestPath = "/v1/_service/" + serviceName + requestPath

	event := []axiom.Event{{ingest.TimestampField: time.Now(), "event": "Service Forward", "from": protocol.MyID, "to": targetPeer, "path": requestPath, "service": serviceName}}
	IngestEvents(event)

	common.Logger.Info("Forwarding request to: ", targetPeer)
	common.Logger.Info("Forwarding path to: ", requestPath)
	target := url.URL{
		Scheme: "libp2p",
		Host:   targetPeer,
		Path:   requestPath,
	}
	director := func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Path = target.Path
		req.URL.Host = req.Host
		req.Host = target.Host
		// Body is already reset on c.Request
	}
	proxy := httputil.NewSingleHostReverseProxy(&target)
	proxy.Director = director
	if strings.HasPrefix(c.Request.Header.Get("Content-Type"), "application/grpc") {
		proxy.Transport = getGlobalH2CTransport()
	} else {
		proxy.Transport = getGlobalTransport()
	}
	proxy.ErrorHandler = ErrorHandler
	proxy.ModifyResponse = func(r *http.Response) error {
		if err := rewriteHeader()(r); err != nil {
			return err
		}
		r.Header.Set("X-Computing-Node", targetPeer)
		return nil
	}

	// Wrap the response writer to handle streaming properly
	streamWriter := &StreamAwareResponseWriter{
		ResponseWriter: c.Writer,
		flusher:        c.Writer.(http.Flusher),
	}

	proxy.ServeHTTP(streamWriter, c.Request)
}
