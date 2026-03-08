package integration_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/nilshah80/aarv"
)

func TestRegisterManyRoutesWithoutConflicts(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))

	const totalRoutes = 60
	for i := 0; i < totalRoutes; i++ {
		path := fmt.Sprintf("/bulk/%02d", i)
		body := fmt.Sprintf("route-%02d", i)

		app.Get(path, func(body string) func(*aarv.Context) error {
			return func(c *aarv.Context) error {
				return c.Text(http.StatusOK, body)
			}
		}(body))
	}

	if got := len(app.Routes()); got != totalRoutes {
		t.Fatalf("expected %d routes, got %d", totalRoutes, got)
	}

	tc := aarv.NewTestClient(app)
	for _, idx := range []int{0, 7, 19, 31, 43, 59} {
		path := fmt.Sprintf("/bulk/%02d", idx)
		resp := tc.Get(path)
		resp.AssertStatus(t, http.StatusOK)
		if got, want := resp.Text(), fmt.Sprintf("route-%02d", idx); got != want {
			t.Fatalf("unexpected response for %s: got %q want %q", path, got, want)
		}
	}

	tc.Get("/bulk/missing").AssertStatus(t, http.StatusNotFound)
}
