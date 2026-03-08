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

// StatusCode returns the HTTP status code associated with the error.
func (e *AppError) StatusCode() int { return e.status }

// Code returns the machine-readable error code.
func (e *AppError) Code() string { return e.code }

// Message returns the client-facing error message.
func (e *AppError) Message() string { return e.message }

// Detail returns the optional detail string included with the error.
func (e *AppError) Detail() string { return e.detail }

// Internal returns the wrapped internal error, if any.
func (e *AppError) Internal() error { return e.internal }

// Unwrap returns the wrapped internal error for errors.Is and errors.As.
func (e *AppError) Unwrap() error { return e.internal }

// NewError creates a custom AppError.
func NewError(status int, code, message string) *AppError {
	return &AppError{status: status, code: code, message: message}
}

// WithDetail sets a detail string on the error and returns the same value.
func (e *AppError) WithDetail(detail string) *AppError {
	e.detail = detail
	return e
}

// WithInternal attaches an internal error that is not serialized to clients.
func (e *AppError) WithInternal(err error) *AppError {
	e.internal = err
	return e
}

// ErrBadRequest creates a 400 bad request AppError.
func ErrBadRequest(msg string) *AppError {
	return &AppError{status: http.StatusBadRequest, code: "bad_request", message: msg}
}

// ErrUnauthorized creates a 401 unauthorized AppError.
func ErrUnauthorized(msg string) *AppError {
	return &AppError{status: http.StatusUnauthorized, code: "unauthorized", message: msg}
}

// ErrForbidden creates a 403 forbidden AppError.
func ErrForbidden(msg string) *AppError {
	return &AppError{status: http.StatusForbidden, code: "forbidden", message: msg}
}

// ErrNotFound creates a 404 not found AppError.
func ErrNotFound(msg string) *AppError {
	return &AppError{status: http.StatusNotFound, code: "not_found", message: msg}
}

// ErrMethodNotAllowed creates a 405 method not allowed AppError.
func ErrMethodNotAllowed(msg string) *AppError {
	return &AppError{status: http.StatusMethodNotAllowed, code: "method_not_allowed", message: msg}
}

// ErrConflict creates a 409 conflict AppError.
func ErrConflict(msg string) *AppError {
	return &AppError{status: http.StatusConflict, code: "conflict", message: msg}
}

// ErrUnprocessable creates a 422 validation failure AppError.
func ErrUnprocessable(msg string) *AppError {
	return &AppError{status: http.StatusUnprocessableEntity, code: "validation_failed", message: msg}
}

// ErrTooManyRequests creates a 429 rate limit AppError.
func ErrTooManyRequests(msg string) *AppError {
	return &AppError{status: http.StatusTooManyRequests, code: "too_many_requests", message: msg}
}

// ErrInternal creates a 500 internal server error AppError.
func ErrInternal(err error) *AppError {
	return &AppError{status: http.StatusInternalServerError, code: "internal_error", message: "Internal server error", internal: err}
}

// ErrBadGateway creates a 502 bad gateway AppError.
func ErrBadGateway(msg string) *AppError {
	return &AppError{status: http.StatusBadGateway, code: "bad_gateway", message: msg}
}

// ErrServiceUnavailable creates a 503 service unavailable AppError.
func ErrServiceUnavailable(msg string) *AppError {
	return &AppError{status: http.StatusServiceUnavailable, code: "service_unavailable", message: msg}
}

// ErrGatewayTimeout creates a 504 gateway timeout AppError.
func ErrGatewayTimeout(msg string) *AppError {
	return &AppError{status: http.StatusGatewayTimeout, code: "gateway_timeout", message: msg}
}

// BindError represents an error during request binding.
type BindError struct {
	// Err is the underlying binding failure.
	Err error
	// Source identifies the binding stage that failed, such as "binding" or "body".
	Source string
}

// Error returns the formatted binding error string.
func (e *BindError) Error() string {
	return fmt.Sprintf("bind error (%s): %v", e.Source, e.Err)
}

// Unwrap returns the underlying binding error.
func (e *BindError) Unwrap() error { return e.Err }

// ValidationError represents a single field validation failure.
type ValidationError struct {
	// Field is the field or parameter name that failed validation.
	Field string `json:"field"`
	// Tag is the validation rule that failed.
	Tag string `json:"tag"`
	// Param is the optional validation rule parameter.
	Param string `json:"param,omitempty"`
	// Value is the rejected value when it is safe to include.
	Value any `json:"value,omitempty"`
	// Message is the human-readable validation message.
	Message string `json:"message"`
}

// ValidationErrors is a collection of validation failures.
type ValidationErrors struct {
	// Errors contains the individual validation failures.
	Errors []ValidationError `json:"details"`
}

// Error returns a summary string for the validation failure set.
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
