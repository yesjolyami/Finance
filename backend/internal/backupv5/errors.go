package backupv5

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidJSON    = errors.New("invalid backup JSON")
	ErrLimit          = errors.New("backup limit exceeded")
	ErrSchema         = errors.New("invalid backup schema")
	ErrValue          = errors.New("invalid backup value")
	ErrReference      = errors.New("invalid backup reference")
	ErrReconciliation = errors.New("backup reconciliation failed")
)

// ValidationError intentionally contains only a stable class and a schema path.
// It must never include a field value or any plaintext financial content.
type ValidationError struct {
	Kind error
	Code string
	Path string
}

func (err *ValidationError) Error() string {
	if err.Path == "" {
		return fmt.Sprintf("backup v5: %s", err.Code)
	}
	return fmt.Sprintf("backup v5: %s at %s", err.Code, err.Path)
}

func (err *ValidationError) Unwrap() error {
	return err.Kind
}

func validationError(kind error, code, path string) error {
	return &ValidationError{Kind: kind, Code: code, Path: path}
}
