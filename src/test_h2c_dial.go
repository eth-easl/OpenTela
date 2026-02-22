package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"

	"golang.org/x/net/http2"
)

func main() {
	t2 := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
			fmt.Printf("DialTLSContext invoked for network=%q addr=%q\n", network, addr)
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	}
	c := &http.Client{Transport: t2}
	// Note: We use an invalid address so it'll fail, but we'll see if DialTLSContext is invoked
	_, err := c.Get("http://127.0.0.1:45678/")
	fmt.Printf("Error: %v\n", err)
}
