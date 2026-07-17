package auth

import (
	"context"
	"errors"
)

var ErrInvalidToken = errors.New("invalid access token")

type Identity struct {
	Subject string
}

type Verifier interface {
	Verify(context.Context, string) (Identity, error)
}
