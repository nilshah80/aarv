// Example: serve aarv over HTTPS with automatic Let's Encrypt certificates,
// paired with an HTTP→HTTPS redirect listener that also satisfies the
// ACME HTTP-01 challenge.
//
// Defaults to Let's Encrypt STAGING (no rate limit, untrusted certs).
// Production switch is intentionally a separate, clearly-labeled block
// at the bottom of main so a copy/paste does not accidentally hit prod.
//
// Run:
//
//	go run . -domain example.com -email ops@example.com
//
// Then point a public DNS A record for the domain at this host's :80 and
// :443. Staging certs are valid for ACME testing only — browsers will
// reject them with a TLS error; that is expected.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/plugins/autocert"
	xautocert "golang.org/x/crypto/acme/autocert"
)

const (
	letsEncryptStagingURL    = "https://acme-staging-v02.api.letsencrypt.org/directory"
	letsEncryptProductionURL = "https://acme-v02.api.letsencrypt.org/directory"
)

func main() {
	domain := flag.String("domain", "", "fully-qualified domain name (required)")
	email := flag.String("email", "", "ACME contact email (recommended)")
	cacheDir := flag.String("cache", "./.autocert-cache", "on-disk cert cache directory")
	prod := flag.Bool("prod", false, "use Let's Encrypt PRODUCTION (rate-limited; only after staging works)")
	flag.Parse()

	if *domain == "" {
		log.Fatal("-domain is required")
	}

	app := aarv.New(aarv.WithBanner(true))
	app.Use(aarv.Recovery(), aarv.Logger())

	app.Get("/", func(c *aarv.Context) error {
		return c.JSON(200, map[string]any{
			"hello": "from aarv autocert",
			"time":  time.Now().UTC(),
		})
	})

	directoryURL := letsEncryptStagingURL
	if *prod {
		// PRODUCTION switch — separate block on purpose so it stays
		// visible to anyone reading or copy-pasting this example.
		directoryURL = letsEncryptProductionURL
		log.Println("WARNING: using Let's Encrypt PRODUCTION endpoint — subject to issuance rate limits")
	}

	cfg := autocert.Config{
		HostPolicy:        xautocert.HostWhitelist(*domain),
		CacheDir:          *cacheDir,
		Email:             *email,
		DirectoryURL:      directoryURL,
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	mgr, err := autocert.Manager(cfg)
	if err != nil {
		log.Fatalf("autocert.Manager: %v", err)
	}

	// Run the HTTP redirect/ACME-HTTP-01 listener on :80 in a goroutine so
	// the autocert manager can satisfy challenges. Sharing one *Manager
	// between the HTTPS server and the redirect handler is the supported
	// flow.
	redirectSrv := autocert.RedirectServer(":80", autocert.RedirectConfig{
		ACMEHandler: mgr,
	})
	go func() {
		log.Println("HTTP redirect listener on :80")
		if err := redirectSrv.ListenAndServe(); err != nil {
			log.Printf("redirect listener exited: %v", err)
		}
	}()

	// Trap SIGINT/SIGTERM so we can shut down the redirect listener
	// alongside the main HTTPS server (which aarv handles via its own
	// lifecycle).
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		<-ch
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = redirectSrv.Shutdown(ctx)
	}()

	fmt.Printf("HTTPS listener on :443 for %s (directory: %s)\n", *domain, directoryURL)
	if err := autocert.ListenWithManager(app, ":443", mgr, cfg); err != nil {
		log.Fatalf("autocert.ListenWithManager: %v", err)
	}
}
