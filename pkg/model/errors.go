package model

import (
	"errors"
)

var (
	ErrAlreadyExists = errors.New("object already exists")
	ErrNotFound      = errors.New("not found")
	ErrQuotaExceeded = errors.New("query limit is exceeded")
	ErrInvalidFeed   = errors.New("invalid feed")
	ErrFeedLimit     = errors.New("web feed limit reached")
)
