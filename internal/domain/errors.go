package domain

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/binbandit/yip/protocol"
)

// Error is a structured, user-readable failure. HTTP handlers and tool
// results both render it as protocol.APIError.
type Error struct {
	Status            int
	Code              string
	Message           string
	Recoverable       bool
	MissingCapability string
	Details           any
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// API converts the error to its wire form.
func (e *Error) API(correlationID string) *protocol.APIError {
	return &protocol.APIError{
		Code:              e.Code,
		Message:           e.Message,
		Recoverable:       e.Recoverable,
		CorrelationID:     correlationID,
		MissingCapability: e.MissingCapability,
		Details:           e.Details,
	}
}

func newErr(status int, code string, recoverable bool, format string, args ...any) *Error {
	return &Error{Status: status, Code: code, Message: fmt.Sprintf(format, args...), Recoverable: recoverable}
}

func NotFound(format string, args ...any) *Error {
	return newErr(http.StatusNotFound, "not_found", false, format, args...)
}

func Invalid(format string, args ...any) *Error {
	return newErr(http.StatusBadRequest, "invalid", true, format, args...)
}

func Forbidden(format string, args ...any) *Error {
	return newErr(http.StatusForbidden, "forbidden", false, format, args...)
}

func Unauthorized(format string, args ...any) *Error {
	return newErr(http.StatusUnauthorized, "unauthorized", true, format, args...)
}

// Conflict is returned for stale versions: a stale click is never accepted.
func Conflict(format string, args ...any) *Error {
	return newErr(http.StatusConflict, "conflict", true, format, args...)
}

func Limit(format string, args ...any) *Error {
	return newErr(http.StatusTooManyRequests, "limit_reached", true, format, args...)
}

func Unavailable(capability, format string, args ...any) *Error {
	e := newErr(http.StatusServiceUnavailable, "unavailable", true, format, args...)
	e.MissingCapability = capability
	return e
}

// Incomplete is returned when completion is claimed without required evidence.
func Incomplete(missing []string, format string, args ...any) *Error {
	e := newErr(http.StatusUnprocessableEntity, "incomplete", true, format, args...)
	e.Details = map[string]any{"missing": missing}
	return e
}

// AsError extracts a *Error, wrapping unknown errors as internal failures.
func AsError(err error) *Error {
	var de *Error
	if errors.As(err, &de) {
		return de
	}
	return &Error{Status: http.StatusInternalServerError, Code: "internal", Message: "Something went wrong on the hub.", Recoverable: true}
}
