package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/plugins/requestid"
	"github.com/nilshah80/aarv/plugins/secure"
)

func main() {
	addr := flag.String("addr", ":8443", "HTTPS listen address")
	cert := flag.String("cert", "server.crt", "TLS certificate path")
	key := flag.String("key", "server.key", "TLS private key path")
	disableH2 := flag.Bool("disable-http2", false, "disable HTTP/2")
	flag.Parse()

	app := aarv.New(
		aarv.WithBanner(true),
		aarv.WithReadHeaderTimeout(5*time.Second),
		aarv.WithReadTimeout(10*time.Second),
		aarv.WithWriteTimeout(30*time.Second),
		aarv.WithIdleTimeout(60*time.Second),
		aarv.WithTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}),
		aarv.WithDisableHTTP2(*disableH2),
	)

	app.Use(
		aarv.Recovery(),
		requestid.New(),
		secure.New(secure.Config{
			HSTSMaxAge:            365 * 24 * 60 * 60,
			HSTSIncludeSubdomains: true,
		}),
	)

	app.Get("/health", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]string{
			"status":   "ok",
			"protocol": c.Protocol(),
		})
	})

	app.Get("/", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]string{
			"message":  "TLS/HTTP2 example",
			"protocol": c.Protocol(),
		})
	})

	fmt.Println("TLS/HTTP2 example")
	fmt.Println("  Generate a local cert with:")
	fmt.Println("  go run /usr/local/go/src/crypto/tls/generate_cert.go -host localhost")
	fmt.Printf("  Serving https://localhost%s\n", *addr)

	log.Fatal(app.ListenTLS(*addr, *cert, *key))
}
