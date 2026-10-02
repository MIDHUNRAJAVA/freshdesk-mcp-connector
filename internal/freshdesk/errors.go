package freshdesk

import (
	"fmt"
	"time"
)

// Kind is the category of a failure. The agent-facing layer maps each kind to
// a plain sentence, so callers never have to parse error text.
type Kind string

const (
	KindAuth         Kind = "auth_failed"
	KindNotFound     Kind = "not_found"
	KindRateLimited  Kind = "rate_limited"
	KindUpstream     Kind = "upstream_error"
	KindUnavailable  Kind = "circuit_open"
	KindBadRequest   Kind = "bad_request"
	KindInvalidInput Kind = "invalid_input"
)

// Error is the only error type the client returns.
type Error struct {
	Kind       Kind
	Status     int           // HTTP status, 0 if none
	Message    string        // safe to show: never contains the API key
	RetryAfter time.Duration // set for rate-limit errors when known
	Err        error         // underlying cause, if any
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("freshdesk %s (HTTP %d): %s", e.Kind, e.Status, e.Message)
	}
	return fmt.Sprintf("freshdesk %s: %s", e.Kind, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

// Is lets errors.Is(err, freshdesk.ErrNotFound) match on Kind alone.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Kind == e.Kind
}

// Sentinels for use with errors.Is.
var (
	ErrAuth        = &Error{Kind: KindAuth}
	ErrNotFound    = &Error{Kind: KindNotFound}
	ErrRateLimited = &Error{Kind: KindRateLimited}
	ErrUpstream    = &Error{Kind: KindUpstream}
	ErrUnavailable = &Error{Kind: KindUnavailable}
	ErrBadRequest  = &Error{Kind: KindBadRequest}
	ErrInvalid     = &Error{Kind: KindInvalidInput}
)

func invalid(format string, args ...any) error {
	return &Error{Kind: KindInvalidInput, Message: fmt.Sprintf(format, args...)}
}
