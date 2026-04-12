package aarv

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// Sentinel errors for SSE operations.
var (
	// ErrSSEClosed is returned when Send, Comment, or Flush is called
	// after Close() has been invoked on the SSEWriter.
	ErrSSEClosed = errors.New("aarv: SSE writer is closed")

	// ErrInvalidSSEField is returned when the Event or ID fields of an
	// SSEEvent contain newlines or carriage returns, which would corrupt
	// the wire format.
	ErrInvalidSSEField = errors.New("aarv: SSE Event or ID field contains invalid newline")
)

// SSEEvent represents a single server-sent event.
// Per the SSE specification, only Data is required; Event, ID, and Retry
// are optional.
type SSEEvent struct {
	// Event is the optional event name (the "event:" field).
	// Must not contain newlines or carriage returns. Omitted from output
	// when empty.
	Event string

	// Data is the event payload (the "data:" field). May contain newlines,
	// in which case it is emitted as multiple "data:" lines per spec.
	Data string

	// ID is the optional event ID (the "id:" field). Clients send this
	// back as Last-Event-ID on reconnect. Must not contain newlines or
	// carriage returns.
	ID string

	// Retry is the optional reconnection delay in milliseconds (the
	// "retry:" field). Omitted when zero or negative.
	Retry int
}

// SSEWriter writes server-sent events to an HTTP response.
// It is obtained from Context.SSE() and must not be shared across
// goroutines.
type SSEWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	ctx     context.Context
	closed  bool
}

// formatSSEEvent serializes an SSEEvent to its wire format.
// Returns ErrInvalidSSEField if Event or ID contains newlines.
func formatSSEEvent(event SSEEvent) ([]byte, error) {
	if strings.ContainsAny(event.Event, "\r\n") {
		return nil, ErrInvalidSSEField
	}
	if strings.ContainsAny(event.ID, "\r\n") {
		return nil, ErrInvalidSSEField
	}

	var buf bytes.Buffer
	if event.Event != "" {
		buf.WriteString("event: ")
		buf.WriteString(event.Event)
		buf.WriteByte('\n')
	}
	if event.ID != "" {
		buf.WriteString("id: ")
		buf.WriteString(event.ID)
		buf.WriteByte('\n')
	}
	if event.Retry > 0 {
		buf.WriteString("retry: ")
		buf.WriteString(strconv.Itoa(event.Retry))
		buf.WriteByte('\n')
	}

	// Data is split on \n; each line is prefixed with "data: ".
	// Per spec, at least one data line is always emitted (even for empty data).
	lines := strings.Split(event.Data, "\n")
	for _, line := range lines {
		buf.WriteString("data: ")
		buf.WriteString(line)
		buf.WriteByte('\n')
	}

	// Blank line terminates the event.
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

// Send writes an event to the response and flushes it to the client.
// Returns ErrSSEClosed if Close has been called, the context error if
// the client has disconnected, ErrInvalidSSEField if Event or ID contain
// newlines, or any underlying write error.
func (w *SSEWriter) Send(event SSEEvent) error {
	if w.closed {
		return ErrSSEClosed
	}
	select {
	case <-w.ctx.Done():
		return w.ctx.Err()
	default:
	}

	data, err := formatSSEEvent(event)
	if err != nil {
		return err
	}
	if _, err := w.w.Write(data); err != nil {
		return err
	}
	if w.flusher != nil {
		w.flusher.Flush()
	}
	return nil
}

// Comment writes an SSE comment (": <text>\n\n") to the response.
// Comments are ignored by EventSource clients but are useful as keepalives
// to prevent intermediate proxies from closing idle connections.
//
// Newlines in text are normalized to spaces to preserve the wire format
// (comments are advisory and non-protocol, so silent normalization is safe).
func (w *SSEWriter) Comment(text string) error {
	if w.closed {
		return ErrSSEClosed
	}
	select {
	case <-w.ctx.Done():
		return w.ctx.Err()
	default:
	}

	// Normalize newlines in comment text
	normalized := strings.NewReplacer("\r", " ", "\n", " ").Replace(text)

	var buf bytes.Buffer
	buf.WriteString(": ")
	buf.WriteString(normalized)
	buf.WriteString("\n\n")

	if _, err := w.w.Write(buf.Bytes()); err != nil {
		return err
	}
	if w.flusher != nil {
		w.flusher.Flush()
	}
	return nil
}

// Flush manually flushes any buffered output to the client without
// sending an event. Returns ErrSSEClosed if Close has been called or the
// context error if the client has disconnected.
func (w *SSEWriter) Flush() error {
	if w.closed {
		return ErrSSEClosed
	}
	select {
	case <-w.ctx.Done():
		return w.ctx.Err()
	default:
	}
	if w.flusher != nil {
		w.flusher.Flush()
	}
	return nil
}

// Close marks the writer as closed. Subsequent Send, Comment, and Flush
// calls return ErrSSEClosed. Close is idempotent.
//
// Close does not release any resources — the HTTP connection is owned
// by the server, not the writer. It is purely a state flag.
func (w *SSEWriter) Close() error {
	w.closed = true
	return nil
}

// Done returns a channel that is closed when the client disconnects
// (when the request context is cancelled). Useful in select loops to
// detect disconnect alongside other channels.
//
// Done remains usable after Close — the caller may still want to know
// whether the context was also cancelled.
func (w *SSEWriter) Done() <-chan struct{} {
	return w.ctx.Done()
}
