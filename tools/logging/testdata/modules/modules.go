// Package modules is the fixture of TestModuleAuditFixture. Every line the
// audit must report carries a `// want` comment quoting part of the violation.
package modules

import (
	"context"
	"os"

	"cosmossdk.io/log"

	"pkg.akt.dev/go/util/ctxlog"

	"github.com/akash-network/provider/tools/logging"
)

type service struct {
	log log.Logger
	raw log.Logger
}

func accepted(ctx context.Context, param log.Logger) {
	ctxlog.LogcFromCtx(ctx).With("module", "fixture").Info("module bound in the chain")

	l := ctxlog.LogcFromCtx(ctx).With("module", "fixture")
	l.Info("module bound on the variable")
	l.With("lid", "x").Info("module bound further up the chain")

	logging.New(os.Stdout, logging.DefaultConfig()).With("module", "fixture").Info("new logger with a module")

	// loggers of unknown origin are not audited
	param.Info("parameter")

	// nor are variables also assigned loggers of another origin
	mixed := ctxlog.LogcFromCtx(ctx)
	if param != nil {
		mixed = param
	}
	mixed.Info("mixed origins")

	s := service{log: ctxlog.LogcFromCtx(ctx).With("module", "fixture")}
	s.log.Info("field with a module")
}

func rejected(ctx context.Context) {
	ctxlog.LogcFromCtx(ctx).Info("context logger")                           // want `logger from ctxlog.LogcFromCtx has no module`
	ctxlog.Logger(ctx).Warn("context logger via Logger")                     // want `logger from ctxlog.Logger has no module`
	ctxlog.LogcFromCtx(ctx).With("lid", "x").Error("chain without a module") // want `logger from ctxlog.LogcFromCtx has no module`
	logging.New(os.Stdout, logging.DefaultConfig()).Debug("new logger")      // want `logger from logging.New has no module`

	l := ctxlog.LogcFromCtx(ctx)
	derived := l.With("lid", "x")
	l.Info("variable")      // want `logger from ctxlog.LogcFromCtx has no module`
	derived.Info("derived") // want `logger from ctxlog.LogcFromCtx has no module`

	s := service{raw: ctxlog.LogcFromCtx(ctx)}
	s.raw.Info("field") // want `logger from ctxlog.LogcFromCtx has no module`
}
