package util

import (
	"os"
	"time"

	"cosmossdk.io/log"
	"github.com/rs/zerolog"
)

func OpenLogger() log.Logger {
	cw := zerolog.ConsoleWriter{
		NoColor:    true,
		TimeFormat: time.Kitchen,
		Out:        os.Stdout,
	}

	// logger with no color output - current debug colors are invisible for me.
	return log.NewCustomLogger(zerolog.New(cw))
}
