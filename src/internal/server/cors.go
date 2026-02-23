package server

import (
	"net/http"
	"opentela/internal/common"

	"github.com/gin-gonic/gin"
)

func corsHeader() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("ocf-version", common.JSONVersion.Commit)
		// if not set
		if c.Writer.Header().Get("Access-Control-Allow-Origin") != "*" {
			c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
			c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
			c.Writer.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
			c.Writer.Header().Set("Access-Control-Allow-Headers", "authorization, origin, content-type, accept")
		}
		if c.Request.Method == "OPTIONS" {
			c.Writer.WriteHeader(http.StatusOK)
		}
	}
}

func rewriteHeader() func(r *http.Response) error {
	return func(r *http.Response) error {
		r.Header.Del("Access-Control-Allow-Origin")
		r.Header.Del("Access-Control-Allow-Credentials")
		r.Header.Del("Access-Control-Allow-Methods")
		r.Header.Del("Access-Control-Allow-Headers")
		return nil
	}
}
