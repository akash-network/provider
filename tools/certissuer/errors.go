package certissuer

import (
	"errors"
)

var (
	ErrConfig      = errors.New("cert issuer: invalid config")
	ErrInvalidArgs = errors.New("cert issuer: invalid args")
)
