// Build-tag-excluded snippet showing how to talk to the h2c listener
// from a Go client. NOT compiled as part of the example binary; copy
// the body into your own client code.

//go:build clientsnippet

package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"

	"golang.org/x/net/http2"
)

func clientSnippet() {
	client := &http.Client{
		Transport: &http2.Transport{
			// AllowHTTP + non-TLS dial = HTTP/2 prior-knowledge over
			// cleartext. Browsers do not exhibit this mode; only
			// programmatic clients (Go, gRPC, k6, etc.) do.
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, addr)
			},
		},
	}

	resp, err := client.Get("http://127.0.0.1:8080/")
	if err != nil {
		fmt.Println("get:", err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("proto=%s status=%d body=%s\n", resp.Proto, resp.StatusCode, body)
}
