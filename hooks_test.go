package aarv

import "testing"

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

func TestHookLifecycleBindPhases(t *testing.T) {
	type req struct {
		Name string `json:"name" validate:"required"`
	}

	app := New(WithBanner(false))
	order := []string{}

	app.AddHook(OnRequest, func(c *Context) error {
		order = append(order, "OnRequest")
		return nil
	})
	app.AddHook(PreRouting, func(c *Context) error {
		order = append(order, "PreRouting")
		return nil
	})
	app.AddHook(PreParsing, func(c *Context) error {
		order = append(order, "PreParsing")
		return nil
	})
	app.AddHook(PreValidation, func(c *Context) error {
		order = append(order, "PreValidation")
		return nil
	})
	app.AddHook(PreHandler, func(c *Context) error {
		order = append(order, "PreHandler")
		return nil
	})
	app.AddHook(OnResponse, func(c *Context) error {
		order = append(order, "OnResponse")
		return nil
	})
	app.AddHook(OnSend, func(c *Context) error {
		order = append(order, "OnSend")
		return nil
	})

	app.Post("/hook", Bind(func(c *Context, req req) (map[string]string, error) {
		order = append(order, "handler")
		return map[string]string{"name": req.Name}, nil
	}))

	resp := NewTestClient(app).Post("/hook", map[string]string{"name": "aarv"})
	resp.AssertStatus(t, 200)

	expected := []string{
		"OnRequest",
		"PreRouting",
		"PreParsing",
		"PreValidation",
		"PreHandler",
		"handler",
		"OnResponse",
		"OnSend",
	}
	if len(order) != len(expected) {
		t.Fatalf("hook lifecycle mismatch: got %v want %v", order, expected)
	}
	for i, want := range expected {
		if order[i] != want {
			t.Fatalf("hook lifecycle[%d] = %q, want %q", i, order[i], want)
		}
	}
}

func TestHookPhaseErrorsPropagateToOnError(t *testing.T) {
	type req struct {
		Name string `json:"name" validate:"required"`
	}

	tests := []struct {
		name      string
		phase     HookPhase
		wantCode  int
		withBody  bool
		wantCalls []string
	}{
		{
			name:      "on request",
			phase:     OnRequest,
			wantCode:  403,
			withBody:  true,
			wantCalls: []string{"OnRequest", "OnError"},
		},
		{
			name:      "pre routing",
			phase:     PreRouting,
			wantCode:  403,
			withBody:  true,
			wantCalls: []string{"OnRequest", "PreRouting", "OnError"},
		},
		{
			name:      "pre parsing",
			phase:     PreParsing,
			wantCode:  403,
			withBody:  true,
			wantCalls: []string{"OnRequest", "PreRouting", "PreParsing", "OnError"},
		},
		{
			name:      "pre validation",
			phase:     PreValidation,
			wantCode:  403,
			withBody:  true,
			wantCalls: []string{"OnRequest", "PreRouting", "PreParsing", "PreValidation", "OnError"},
		},
		{
			name:      "pre handler",
			phase:     PreHandler,
			wantCode:  403,
			withBody:  true,
			wantCalls: []string{"OnRequest", "PreRouting", "PreParsing", "PreValidation", "PreHandler", "OnError"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := New(WithBanner(false))
			calls := []string{}
			handlerCalled := false
			var seenErr error

			add := func(phase HookPhase, name string) {
				app.AddHook(phase, func(c *Context) error {
					calls = append(calls, name)
					if phase == tt.phase {
						return ErrForbidden("hook blocked")
					}
					return nil
				})
			}

			add(OnRequest, "OnRequest")
			add(PreRouting, "PreRouting")
			add(PreParsing, "PreParsing")
			add(PreValidation, "PreValidation")
			add(PreHandler, "PreHandler")
			app.AddHook(OnError, func(c *Context) error {
				calls = append(calls, "OnError")
				seenErr = c.HookError()
				return nil
			})

			app.Post("/hook", Bind(func(c *Context, req req) (map[string]string, error) {
				handlerCalled = true
				return map[string]string{"name": req.Name}, nil
			}))

			resp := NewTestClient(app).Post("/hook", map[string]string{"name": "aarv"})
			resp.AssertStatus(t, tt.wantCode)
			if handlerCalled {
				t.Fatal("handler should not have run")
			}
			if seenErr == nil || seenErr.Error() != "hook blocked" {
				t.Fatalf("expected OnError to receive propagated hook error, got %v", seenErr)
			}
			if len(calls) != len(tt.wantCalls) {
				t.Fatalf("calls mismatch: got %v want %v", calls, tt.wantCalls)
			}
			for i, want := range tt.wantCalls {
				if calls[i] != want {
					t.Fatalf("calls[%d] = %q, want %q (full=%v)", i, calls[i], want, calls)
				}
			}
		})
	}
}

func TestOnErrorRunsWithCustomErrorHandler(t *testing.T) {
	app := New(
		WithBanner(false),
		WithErrorHandler(func(c *Context, err error) {
			_ = c.Text(418, "custom")
		}),
	)

	var seen error
	app.AddHook(OnError, func(c *Context) error {
		seen = c.HookError()
		return nil
	})
	app.AddHook(OnRequest, func(c *Context) error {
		return ErrBadRequest("boom")
	})
	app.Get("/test", func(c *Context) error {
		return c.Text(200, "ok")
	})

	resp := NewTestClient(app).Get("/test")
	resp.AssertStatus(t, 418)
	if seen == nil || seen.Error() != "boom" {
		t.Fatalf("expected OnError to run before custom error handler, got %v", seen)
	}
}

func TestPreHandlerRunsForPlainHandlers(t *testing.T) {
	app := New(WithBanner(false))
	order := []string{}

	app.AddHook(PreHandler, func(c *Context) error {
		order = append(order, "PreHandler")
		return nil
	})
	app.Get("/plain", func(c *Context) error {
		order = append(order, "handler")
		return c.Text(200, "ok")
	})

	resp := NewTestClient(app).Get("/plain")
	resp.AssertStatus(t, 200)

	expected := []string{"PreHandler", "handler"}
	if len(order) != len(expected) {
		t.Fatalf("plain handler order mismatch: got %v want %v", order, expected)
	}
	for i, want := range expected {
		if order[i] != want {
			t.Fatalf("plain handler order[%d] = %q, want %q", i, order[i], want)
		}
	}
}

func TestPreRoutingRunsOnDirectFastPath(t *testing.T) {
	app := New(WithBanner(false))
	order := []string{}

	app.AddHook(PreRouting, func(c *Context) error {
		order = append(order, "PreRouting")
		return nil
	})
	app.Get("/fast", func(c *Context) error {
		order = append(order, "handler")
		return c.Text(200, "ok")
	})

	resp := NewTestClient(app).Get("/fast")
	resp.AssertStatus(t, 200)

	expected := []string{"PreRouting", "handler"}
	if len(order) != len(expected) {
		t.Fatalf("fast path order mismatch: got %v want %v", order, expected)
	}
	for i, want := range expected {
		if order[i] != want {
			t.Fatalf("fast path order[%d] = %q, want %q", i, order[i], want)
		}
	}
}
