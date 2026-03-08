package aarv

import "sort"

// HookPhase represents a phase in the request lifecycle.
type HookPhase int

const (
	// OnRequest runs immediately after the framework acquires the request context.
	OnRequest HookPhase = iota
	// PreRouting is reserved for hooks that should run before route matching.
	PreRouting
	// PreParsing is reserved for hooks that should run before body parsing.
	PreParsing
	// PreValidation is reserved for hooks that should run after parsing and before validation.
	PreValidation
	// PreHandler is reserved for hooks that should run after validation and before the handler.
	PreHandler
	// OnResponse runs after the handler completes and before the final response lifecycle ends.
	OnResponse
	// OnSend runs just before buffered response bytes are flushed to the client.
	OnSend
	// OnError runs when the framework handles an error returned from the chain.
	OnError
	// OnStartup runs before the server begins accepting requests.
	OnStartup
	// OnShutdown runs when graceful shutdown starts.
	OnShutdown
)

// HookFunc is a lifecycle hook function.
type HookFunc func(c *Context) error

type hookEntry struct {
	priority int
	fn       HookFunc
}

type hookRegistry struct {
	hooks map[HookPhase][]hookEntry
}

func newHookRegistry() *hookRegistry {
	return &hookRegistry{
		hooks: make(map[HookPhase][]hookEntry),
	}
}

func (hr *hookRegistry) add(phase HookPhase, fn HookFunc) {
	hr.addWithPriority(phase, 0, fn)
}

func (hr *hookRegistry) addWithPriority(phase HookPhase, priority int, fn HookFunc) {
	hr.hooks[phase] = append(hr.hooks[phase], hookEntry{priority: priority, fn: fn})
}

// finalize sorts all hooks by priority. Call once before serving requests.
func (hr *hookRegistry) finalize() {
	for phase, entries := range hr.hooks {
		sort.SliceStable(entries, func(i, j int) bool {
			return entries[i].priority < entries[j].priority
		})
		hr.hooks[phase] = entries
	}
}

func (hr *hookRegistry) run(phase HookPhase, c *Context) error {
	entries := hr.hooks[phase]
	for _, entry := range entries {
		if err := entry.fn(c); err != nil {
			return err
		}
	}
	return nil
}

// ShutdownHook runs during graceful shutdown with the shutdown context.
type ShutdownHook func(ctx interface{ Done() <-chan struct{} }) error
