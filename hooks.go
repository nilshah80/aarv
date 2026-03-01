package aarv

import "sort"

// HookPhase represents a phase in the request lifecycle.
type HookPhase int

const (
	OnRequest     HookPhase = iota // Immediately after request received
	PreRouting                     // Before route matching
	PreParsing                     // Before body parsing
	PreValidation                  // After parsing, before validation
	PreHandler                     // After validation, before handler
	OnResponse                     // After handler, before sending
	OnSend                         // Just before bytes go to wire
	OnError                        // On any error in the chain
	OnStartup                      // Server starts listening
	OnShutdown                     // Server shutdown initiated
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

// ShutdownHook is called during graceful shutdown.
type ShutdownHook func(ctx interface{ Done() <-chan struct{} }) error
