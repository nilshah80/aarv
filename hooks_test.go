package aarv

import (
	"testing"
)

func TestHooks(t *testing.T) {
	app := New(WithBanner(false))

	execOrder := []string{}

	app.AddHookWithPriority(OnRequest, 1, func(c *Context) error {
		execOrder = append(execOrder, "req_1")
		return nil
	})

	app.AddHookWithPriority(OnRequest, 10, func(c *Context) error {
		execOrder = append(execOrder, "req_10")
		return nil
	})

	app.AddHook(OnResponse, func(c *Context) error {
		execOrder = append(execOrder, "res_def")
		return nil
	})

	app.AddHook(OnSend, func(c *Context) error {
		execOrder = append(execOrder, "send_def")
		return nil
	})

	app.Get("/hook", func(c *Context) error {
		execOrder = append(execOrder, "handler")
		return c.Text(200, "ok")
	})

	tc := NewTestClient(app)
	resp := tc.Get("/hook")
	resp.AssertStatus(t, 200)

	expected := []string{"req_1", "req_10", "handler", "res_def", "send_def"}
	if len(execOrder) != len(expected) {
		t.Fatalf("Hooks execution order mismatch. Got %v, expected %v", execOrder, expected)
	}

	for i, v := range expected {
		if execOrder[i] != v {
			t.Errorf("Mismatch at index %d: Got %v, expected %v", i, execOrder[i], v)
		}
	}
}

func TestHookErrorShortCircuit(t *testing.T) {
	app := New(WithBanner(false))

	app.AddHook(OnRequest, func(c *Context) error {
		return c.Error(403, "hook error")
	})

	called := false
	app.Get("/test", func(c *Context) error {
		called = true
		return c.Text(200, "ok")
	})

	tc := NewTestClient(app)
	resp := tc.Get("/test")
	resp.AssertStatus(t, 403)

	if called {
		t.Errorf("Handler should not have been called")
	}
}
