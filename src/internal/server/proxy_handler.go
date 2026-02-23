package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"opentela/internal/common"
	"opentela/internal/protocol"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/axiomhq/axiom-go/axiom"
	"github.com/axiomhq/axiom-go/axiom/ingest"
	"github.com/buger/jsonparser"
	"github.com/gin-gonic/gin"
	p2phttp "github.com/libp2p/go-libp2p-http"
)

var (
	globalTransport *http.Transport
	transportOnce   sync.Once
)

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
		s.ResponseWriter.Header().Set("Connection", "keep-alive")
		s.ResponseWriter.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering
	}
	s.ResponseWriter.WriteHeader(statusCode)
}

func (s *StreamAwareResponseWriter) Flush() {
	if s.flusher != nil {
		s.flusher.Flush()
	}
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
	proxy.Transport = getGlobalTransport()
	proxy.ErrorHandler = ErrorHandler
	proxy.ModifyResponse = rewriteHeader()
	proxy.ServeHTTP(c.Writer, c.Request)
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
	// Use global transport here too if we want pooling to external HTTP services,
	// though standard http.DefaultTransport also pools.
	// However, if we want shared settings (timeouts), we can use ours.
	// NOTE: standard http transport doesn't support libp2p.
	// Ideally we separate p2p transport from standard http transport, OR register protocols on one.
	// Our getGlobalTransport() has registered libp2p, so it works for both (http falls back to standard).
	proxy.Transport = getGlobalTransport()

	proxy.ServeHTTP(c.Writer, c.Request)
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
	// Candidates are grouped by match priority: exact > wildcard > catch-all.
	// We only pick from the highest-priority non-empty tier so that wildcard
	// and catch-all providers are used only when no exact match is available.
	var exactCandidates, wildcardCandidates, catchAllCandidates []string
	for _, provider := range providers {
		for _, service := range provider.Service {
			if service.Name == serviceName {
				// Track the best (highest-priority) match for this provider.
				// 0 = no match, 1 = catch-all, 2 = wildcard, 3 = exact
				bestMatch := 0
				if len(service.IdentityGroup) > 0 {
					for _, ig := range service.IdentityGroup {
						// "all" is a shortcut that matches every request
						if ig == "all" {
							if bestMatch < 1 {
								bestMatch = 1
							}
							continue
						}
						igGroup := strings.Split(ig, "=")
						if len(igGroup) != 2 {
							continue
						}
						igKey := igGroup[0]
						igValue := igGroup[1]
						// "*" wildcard: match if the key exists in the request body (any value)
						if igValue == "*" {
							if _, _, _, err := jsonparser.Get(body, igKey); err == nil {
								if bestMatch < 2 {
									bestMatch = 2
								}
							}
							continue
						}
						// exact match
						requestGroup, err := jsonparser.GetString(body, igKey)
						if err == nil && requestGroup == igValue {
							bestMatch = 3
							break // can't do better than exact
						}
					}
				}
				switch bestMatch {
				case 3:
					exactCandidates = append(exactCandidates, provider.ID)
				case 2:
					wildcardCandidates = append(wildcardCandidates, provider.ID)
				case 1:
					catchAllCandidates = append(catchAllCandidates, provider.ID)
				}
			}
		}
	}
	// Determine fallback level from the X-Otela-Fallback request header.
	// 0 (default): exact match only
	// 1: allow wildcard fallback when no exact match exists
	// 2: allow wildcard + catch-all fallback
	fallbackLevel := 0
	if fbHeader := c.GetHeader("X-Otela-Fallback"); fbHeader != "" {
		if lvl, err := strconv.Atoi(fbHeader); err == nil && lvl >= 0 && lvl <= 2 {
			fallbackLevel = lvl
		}
	}

	// Pick from the highest-priority non-empty tier, respecting fallback level
	candidates := exactCandidates
	if len(candidates) == 0 && fallbackLevel >= 1 {
		candidates = wildcardCandidates
	}
	if len(candidates) == 0 && fallbackLevel >= 2 {
		candidates = catchAllCandidates
	}
	if len(candidates) == 0 {
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
	proxy.Transport = getGlobalTransport()
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
