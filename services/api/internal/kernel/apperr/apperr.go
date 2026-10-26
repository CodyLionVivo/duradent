// Package apperr defines the error kinds every bounded context returns, so
// HTTP adapters can map them to status codes without knowing each domain.
package apperr

import (
	"errors"
	"fmt"
)

type Kind int

const (
	KindInvalid  Kind = iota + 1 // input breaks a business rule → 422
	KindNotFound                 // → 404
	KindConflict                 // duplicate or state conflict → 409
)

type Error struct {
	Kind  Kind
	Code  string // stable machine code, e.g. "catalog.slug_taken"
	Field string // offending input field, if any
	Msg   string
}

func (e *Error) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Code, e.Msg, e.Field)
	}
	return e.Code + ": " + e.Msg
}

func Invalid(code, field, msg string) *Error {
	return &Error{Kind: KindInvalid, Code: code, Field: field, Msg: msg}
}

func NotFound(code, msg string) *Error {
	return &Error{Kind: KindNotFound, Code: code, Msg: msg}
}

func Conflict(code, field, msg string) *Error {
	return &Error{Kind: KindConflict, Code: code, Field: field, Msg: msg}
}

// As extracts an *Error from err's chain.
func As(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

// IsKind reports whether err carries an *Error of kind k.
func IsKind(err error, k Kind) bool {
	e, ok := As(err)
	return ok && e.Kind == k
}
