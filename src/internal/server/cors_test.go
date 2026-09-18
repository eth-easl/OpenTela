package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"opentela/internal/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCorsHeader_SetsCORSHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(corsHeader())
	router.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req, err := http.NewRequest("GET", "/test", nil)
	require.NoError(t, err)

	router.ServeHTTP(w, req)

	assert.Equal(t, common.JSONVersion.Commit, w.Header().Get("ocf-version"))
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", w.Header().Get("Access-Control-Allow-Credentials"))
	assert.Equal(t, "GET,POST,PUT,PATCH,DELETE,OPTIONS", w.Header().Get("Access-Control-Allow-Methods"))
	assert.Equal(t, "authorization, origin, content-type, accept, X-Otela-Fallback, X-Otela-Trust, X-Session-Affinity", w.Header().Get("Access-Control-Allow-Headers"))
}

func TestCorsHeader_OptionsReturns200(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(corsHeader())
	// The handler does not write its own status, so the middleware's
	// WriteHeader(200) for OPTIONS is the effective response code.
	router.Any("/test", func(c *gin.Context) {})

	w := httptest.NewRecorder()
	req, err := http.NewRequest("OPTIONS", "/test", nil)
	require.NoError(t, err)

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	// CORS headers should also be present on OPTIONS responses.
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
}

func TestCorsHeader_DoesNotOverrideExisting(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	// Pre-set Access-Control-Allow-Origin to "*" before the CORS middleware runs.
	router.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Next()
	})
	router.Use(corsHeader())
	router.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req, err := http.NewRequest("GET", "/test", nil)
	require.NoError(t, err)

	router.ServeHTTP(w, req)

	// ocf-version should always be set
	assert.Equal(t, common.JSONVersion.Commit, w.Header().Get("ocf-version"))
	// Origin stays "*" but the other CORS headers should NOT be set
	// because the corsHeader function skips setting when ACAO is already "*".
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"))
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Methods"))
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Headers"))
}

func TestRewriteHeader_RemovesCORSHeaders(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{
			"Access-Control-Allow-Origin":      {"*"},
			"Access-Control-Allow-Credentials": {"true"},
			"Access-Control-Allow-Methods":     {"GET,POST"},
			"Access-Control-Allow-Headers":     {"authorization"},
			"Content-Type":                     {"application/json"},
		},
	}

	rewrite := rewriteHeader()
	err := rewrite(resp)
	require.NoError(t, err)

	assert.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
	assert.Empty(t, resp.Header.Get("Access-Control-Allow-Credentials"))
	assert.Empty(t, resp.Header.Get("Access-Control-Allow-Methods"))
	assert.Empty(t, resp.Header.Get("Access-Control-Allow-Headers"))
	// Non-CORS headers should be preserved.
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
}
