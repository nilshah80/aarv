// Example: ListenTLS with WithCertReload watching the cert/key files
// for changes (mtime/size polling, no fsnotify dependency).
//
// Run gencerts.sh first to produce ./certs/server.crt + ./certs/server.key,
// then:
//
//	go run . -addr 127.0.0.1:8443
//
// In a second terminal, regenerate the cert and watch for the
// "cert reloaded" log line within one poll interval:
//
//	./gencerts.sh && touch certs/server.crt certs/server.key
//
// You can also overwrite the cert file with a malformed PEM to verify
// the WARN log + previous-cert preservation behavior.
package main

import (
	"flag"
	"log"
	"time"

	"github.com/nilshah80/aarv"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8443", "HTTPS bind address")
	cert := flag.String("cert", "./certs/server.crt", "PEM cert path")
	key := flag.String("key", "./certs/server.key", "PEM key path")
	interval := flag.Duration("interval", 5*time.Second, "cert reload poll interval (min 1s)")
	flag.Parse()

	app := aarv.New(
		aarv.WithBanner(true),
		aarv.WithCertReload(*interval),
	)
	app.Use(aarv.Recovery(), aarv.Logger())

	app.Get("/", func(c *aarv.Context) error {
		return c.JSON(200, map[string]any{
			"hello": "from aarv with hot-reloaded certificate",
			"time":  time.Now().UTC(),
		})
	})

	log.Printf("HTTPS on %s (cert=%s, key=%s, reload interval=%v)", *addr, *cert, *key, *interval)
	log.Println("In another terminal, regenerate the cert with ./gencerts.sh and watch for the 'cert reloaded' log line.")
	if err := app.ListenTLS(*addr, *cert, *key); err != nil {
		log.Fatal(err)
	}
}
