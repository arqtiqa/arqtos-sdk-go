// Package cerr is the connector error taxonomy. Contract methods return *Error;
// callers classify with KindOf/Retryable and never string-match.
package cerr

import (
	"errors"
	"fmt"
)

type Kind int

const (
	KindUnknown Kind = iota
	KindNotFound
	KindUnauthorized
	KindUnavailable
	KindUnsupported
	KindInvalid
	KindTimeout
)

func (k Kind) String() string {
	switch k {
	case KindNotFound:
		return "not_found"
	case KindUnauthorized:
		return "unauthorized"
	case KindUnavailable:
		return "unavailable"
	case KindUnsupported:
		return "unsupported"
	case KindInvalid:
		return "invalid"
	case KindTimeout:
		return "timeout"
	default:
		return "unknown"
	}
}

type Error struct {
	Kind Kind
	Op   string // the contract op, e.g. "Resolve"
	Err  error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Op, e.Kind, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Op, e.Kind)
}

func (e *Error) Unwrap() error { return e.Err }

func New(kind Kind, op string, err error) *Error { return &Error{Kind: kind, Op: op, Err: err} }

// KindOf returns the Kind of the first *Error in the chain, else KindUnknown.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindUnknown
}

// Retryable reports whether err is transient (Unavailable or Timeout).
func Retryable(err error) bool {
	k := KindOf(err)
	return k == KindUnavailable || k == KindTimeout
}
