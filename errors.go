package aarv

import (
	"errors"
	"fmt"
	"net/http"
)

// Sentinel errors for common conditions.
var (
	// ErrResponseAlreadyWritten is returned when attempting to write to a response
	// that has already been written to.
	ErrResponseAlreadyWritten = errors.New("aarv: response already written")
)

// AppError is a structured HTTP error with status code, machine-readable code, and message.
type AppError struct {
	status   int
	code     string
	message  string
	detail   string
	internal error
}

func (e *AppError) Error() string {
	if e.internal != nil {
		return fmt.Sprintf("%s: %v", e.message, e.internal)
	}
	return e.message
}

func (e *AppError) StatusCode() int { return e.status }
func (e *AppError) Code() string    { return e.code }
func (e *AppError) Message() string { return e.message }
func (e *AppError) Detail() string  { return e.detail }
func (e *AppError) Internal() error { return e.internal }
func (e *AppError) Unwrap() error   { return e.internal }

// NewError creates a custom AppError.
func NewError(status int, code, message string) *AppError {
	return &AppError{status: status, code: code, message: message}
}

// WithDetail adds a detail string to the error.
func (e *AppError) WithDetail(detail string) *AppError {
	e.detail = detail
	return e
}

// WithInternal wraps an internal error (not serialized to client).
func (e *AppError) WithInternal(err error) *AppError {
	e.internal = err
	return e
}

func ErrBadRequest(msg string) *AppError {
	return &AppError{status: http.StatusBadRequest, code: "bad_request", message: msg}
}

func ErrUnauthorized(msg string) *AppError {
	return &AppError{status: http.StatusUnauthorized, code: "unauthorized", message: msg}
}

func ErrForbidden(msg string) *AppError {
	return &AppError{status: http.StatusForbidden, code: "forbidden", message: msg}
}

func ErrNotFound(msg string) *AppError {
	return &AppError{status: http.StatusNotFound, code: "not_found", message: msg}
}

func ErrMethodNotAllowed(msg string) *AppError {
	return &AppError{status: http.StatusMethodNotAllowed, code: "method_not_allowed", message: msg}
}

func ErrConflict(msg string) *AppError {
	return &AppError{status: http.StatusConflict, code: "conflict", message: msg}
}

func ErrUnprocessable(msg string) *AppError {
	return &AppError{status: http.StatusUnprocessableEntity, code: "validation_failed", message: msg}
}

func ErrTooManyRequests(msg string) *AppError {
	return &AppError{status: http.StatusTooManyRequests, code: "too_many_requests", message: msg}
}

func ErrInternal(err error) *AppError {
	return &AppError{status: http.StatusInternalServerError, code: "internal_error", message: "Internal server error", internal: err}
}

func ErrBadGateway(msg string) *AppError {
	return &AppError{status: http.StatusBadGateway, code: "bad_gateway", message: msg}
}

func ErrServiceUnavailable(msg string) *AppError {
	return &AppError{status: http.StatusServiceUnavailable, code: "service_unavailable", message: msg}
}

func ErrGatewayTimeout(msg string) *AppError {
	return &AppError{status: http.StatusGatewayTimeout, code: "gateway_timeout", message: msg}
}

// BindError represents an error during request binding.
type BindError struct {
	Err    error
	Source string
}

func (e *BindError) Error() string {
	return fmt.Sprintf("bind error (%s): %v", e.Source, e.Err)
}

func (e *BindError) Unwrap() error { return e.Err }

// ValidationError represents a single field validation failure.
type ValidationError struct {
	Field   string `json:"field"`
	Tag     string `json:"tag"`
	Param   string `json:"param,omitempty"`
	Value   any    `json:"value,omitempty"`
	Message string `json:"message"`
}

// ValidationErrors is a collection of validation failures.
type ValidationErrors struct {
	Errors []ValidationError `json:"details"`
}

func (e *ValidationErrors) Error() string {
	return fmt.Sprintf("validation failed: %d error(s)", len(e.Errors))
}

// ErrorHandler is a function that handles errors returned by handlers.
type ErrorHandler func(c *Context, err error)

// ErrorResponse is the JSON structure for error responses.
type errorResponse struct {
	Error     string `json:"error"`
	Message   string `json:"message"`
	Detail    string `json:"detail,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}
