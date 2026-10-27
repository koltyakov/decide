// Package fault defines machine-readable failures shared by CLI and MCP.
package fault

import "errors"

const (
	Failure  = 1
	Usage    = 2
	Auth     = 3
	Remote   = 4
	Rejected = 5
	Batch    = 6
)

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"error"`
}

func (e *Error) Error() string { return e.Message }

func New(code int, message string) error { return &Error{Code: code, Message: message} }

func Code(err error) int {
	var failure *Error
	if errors.As(err, &failure) {
		return failure.Code
	}
	return Failure
}
