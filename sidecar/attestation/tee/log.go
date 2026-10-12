package tee

import (
	"sync/atomic"

	"github.com/akash-network/attestation-sidecar/internal/logging"
)

var pkgLogger atomic.Value // holds loggerBox

type loggerBox struct{ logging.Logger }

// SetLogger sets the logger used by TEE providers. nil disables logging.
func SetLogger(l logging.Logger) {
	if l == nil {
		l = logging.Nop()
	}

	pkgLogger.Store(loggerBox{l})
}

func logger() logging.Logger {
	if b, ok := pkgLogger.Load().(loggerBox); ok {
		return b.Logger
	}

	return logging.Nop()
}
