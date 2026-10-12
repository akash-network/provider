package logging

import (
	"fmt"
	"io"
	"os"
	"reflect"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger is the sidecar's structured logger. Key/value pairs follow the provider's
// canonical snake_case keys; "module" names the component and is set via With.
type Logger interface {
	Debug(msg string, keyVals ...any)
	Info(msg string, keyVals ...any)
	Warn(msg string, keyVals ...any)
	Error(msg string, keyVals ...any)
	With(keyVals ...any) Logger
}

// KeyModule names the component emitting the entry.
const KeyModule = "module"

const (
	keyMessage    = "message"
	keyLevel      = "level"
	keyTime       = "time"
	keyStacktrace = "stacktrace"
	keyBad        = "!BADKEY"
)

type logger struct {
	cfg *Config
	// base carries context fields but never a module name
	base *zap.Logger
	// out is base named after the current module (or base when there is none)
	out    *zap.Logger
	module string
	// min is the level applied to entries of the current module
	min zapcore.Level
	// floor is the lowest level any entry may be logged at
	floor zapcore.Level
}

var _ Logger = (*logger)(nil)

// New returns a logger writing to w (os.Stdout when w is nil) configured by cfg.
//
// The returned logger keeps exactly one "module" field: With("module", x) on a
// logger that already has a module replaces it. Level and per-module filters are
// evaluated before an entry is encoded.
func New(w io.Writer, cfg Config) Logger {
	if w == nil {
		w = os.Stdout
	}

	ec := zapcore.EncoderConfig{
		MessageKey:       keyMessage,
		LevelKey:         keyLevel,
		NameKey:          KeyModule,
		StacktraceKey:    keyStacktrace,
		LineEnding:       zapcore.DefaultLineEnding,
		EncodeDuration:   zapcore.StringDurationEncoder,
		EncodeName:       zapcore.FullNameEncoder,
		ConsoleSeparator: " ",
	}

	switch cfg.Timestamp {
	case TimestampRFC3339:
		ec.TimeKey = keyTime
		ec.EncodeTime = zapcore.RFC3339TimeEncoder
	case TimestampRFC3339Nano:
		ec.TimeKey = keyTime
		ec.EncodeTime = zapcore.RFC3339NanoTimeEncoder
	case TimestampKitchen:
		ec.TimeKey = keyTime
		ec.EncodeTime = zapcore.TimeEncoderOfLayout(time.Kitchen)
	}

	var enc zapcore.Encoder
	if cfg.Format == FormatJSON {
		ec.EncodeLevel = zapcore.LowercaseLevelEncoder
		enc = zapcore.NewJSONEncoder(ec)
	} else {
		ec.EncodeLevel = zapcore.CapitalLevelEncoder
		if cfg.Color {
			ec.EncodeLevel = zapcore.CapitalColorLevelEncoder
		}
		enc = zapcore.NewConsoleEncoder(ec)
	}

	floor := cfg.minLevel()
	core := zapcore.NewCore(enc, zapcore.Lock(zapcore.AddSync(w)), floor)

	var opts []zap.Option
	if cfg.Trace {
		opts = append(opts, zap.AddStacktrace(zapcore.ErrorLevel))
	}

	zl := zap.New(core, opts...)
	c := cfg

	return &logger{
		cfg:   &c,
		base:  zl,
		out:   zl,
		min:   c.Level,
		floor: floor,
	}
}

func (l *logger) Debug(msg string, keyVals ...any) { l.log(zapcore.DebugLevel, msg, keyVals) }
func (l *logger) Info(msg string, keyVals ...any)  { l.log(zapcore.InfoLevel, msg, keyVals) }
func (l *logger) Warn(msg string, keyVals ...any)  { l.log(zapcore.WarnLevel, msg, keyVals) }
func (l *logger) Error(msg string, keyVals ...any) { l.log(zapcore.ErrorLevel, msg, keyVals) }

// With returns a logger with additional context. A "module" key replaces the
// current module instead of adding a second "module" field.
func (l *logger) With(keyVals ...any) Logger {
	module, hasModule, rest := splitModule(keyVals)

	nl := *l
	if len(rest) > 0 {
		nl.base = l.base.With(l.fields(rest)...)
	}

	if hasModule {
		nl.module = module
		nl.min = l.cfg.moduleLevel(module)
	}

	nl.out = nl.base
	if nl.module != "" {
		nl.out = nl.base.Named(nl.module)
	}

	return &nl
}

func (l *logger) log(lvl zapcore.Level, msg string, keyVals []any) {
	if lvl < l.floor {
		return
	}

	out := l.out
	min := l.min

	if module, ok, rest := splitModule(keyVals); ok {
		out = l.base.Named(module)
		min = l.cfg.moduleLevel(module)
		keyVals = rest
	}

	if lvl < min {
		return
	}

	if ce := out.Check(lvl, msg); ce != nil {
		ce.Write(l.fields(keyVals)...)
	}
}

func (l *logger) fields(keyVals []any) []zap.Field {
	if len(keyVals) == 0 {
		return nil
	}

	fields := make([]zap.Field, 0, (len(keyVals)+1)/2)

	for i := 0; i < len(keyVals); i += 2 {
		if i+1 == len(keyVals) {
			fields = append(fields, zap.Any(keyBad, keyVals[i]))
			break
		}

		fields = append(fields, l.field(keyString(keyVals[i]), keyVals[i+1]))
	}

	return fields
}

func (l *logger) field(key string, val any) zap.Field {
	// zap encodes errors implementing fmt.Formatter with an additional "<key>Verbose"
	// field carrying the full stack. Keep the plain message unless traces are enabled.
	if err, ok := val.(error); ok && !l.cfg.Trace {
		return zap.String(key, safeError(err))
	}

	return zap.Any(key, val)
}

// splitModule extracts the last "module" pair from keyVals.
// rest aliases keyVals when no module key is present.
func splitModule(keyVals []any) (module string, found bool, rest []any) {
	idx := -1
	for i := 0; i+1 < len(keyVals); i += 2 {
		if keyString(keyVals[i]) == KeyModule {
			idx = i
			found = true
		}
	}

	if !found {
		return "", false, keyVals
	}

	if s, ok := keyVals[idx+1].(string); ok {
		module = s
	} else {
		module = fmt.Sprint(keyVals[idx+1])
	}

	rest = make([]any, 0, len(keyVals))
	for i := 0; i < len(keyVals); i += 2 {
		if i+1 < len(keyVals) && keyString(keyVals[i]) == KeyModule {
			continue
		}

		rest = append(rest, keyVals[i])
		if i+1 < len(keyVals) {
			rest = append(rest, keyVals[i+1])
		}
	}

	return module, true, rest
}

// keyString normalizes a key the same way for module extraction and field encoding.
func keyString(key any) string {
	if s, ok := key.(string); ok {
		return s
	}

	return fmt.Sprint(key)
}

// safeError returns err.Error(), or "<nil>" for typed-nil pointers whose Error panics.
func safeError(err error) (res string) {
	defer func() {
		if r := recover(); r != nil {
			if v := reflect.ValueOf(err); v.Kind() == reflect.Pointer && v.IsNil() {
				res = "<nil>"
				return
			}

			res = fmt.Sprintf("<PANIC=%v>", r)
		}
	}()

	return err.Error()
}

type nopLogger struct{}

// Nop returns a logger discarding all entries.
func Nop() Logger { return nopLogger{} }

func (nopLogger) Debug(string, ...any) {}
func (nopLogger) Info(string, ...any)  {}
func (nopLogger) Warn(string, ...any)  {}
func (nopLogger) Error(string, ...any) {}
func (n nopLogger) With(...any) Logger { return n }
