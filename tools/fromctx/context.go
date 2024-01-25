package fromctx

import (
	"context"

	"github.com/boz/go-lifecycle"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"github.com/troian/pubsub"
	"golang.org/x/sync/errgroup"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	cmblog "github.com/tendermint/tendermint/libs/log"

	providerflags "github.com/akash-network/provider/cmd/provider-services/cmd/flags"
	akashclientset "github.com/akash-network/provider/pkg/client/clientset/versioned"
)

type Key string

const (
	CtxKeyKubeConfig     = Key(providerflags.FlagKubeConfig)
	CtxKeyKubeClientSet  = Key("kube-clientset")
	CtxKeyAkashClientSet = Key("akash-clientset")
	CtxKeyPubSub         = Key("pubsub")
	CtxKeyLifecycle      = Key("lifecycle")
	CtxKeyErrGroup       = Key("errgroup")
	CtxKeyLogc           = Key("logc")
	CtxKeyStartupCh      = Key("startup-ch")
)

type options struct {
	logName Key
}

type LogcOption func(*options) error

type dummyLogger struct{}

func (l *dummyLogger) Debug(_ string, _ ...interface{}) {}
func (l *dummyLogger) Info(_ string, _ ...interface{})  {}
func (l *dummyLogger) Error(_ string, _ ...interface{}) {}
func (l *dummyLogger) With(_ ...interface{}) cmblog.Logger {
	return &dummyLogger{}
}

func CmdSetContextValue(cmd *cobra.Command, key, val interface{}) {
	cmd.SetContext(context.WithValue(cmd.Context(), key, val))
}

// WithLogc add logger object to the context
// key defaults to the "log"
// use WithLogName("<custom name>") to set custom key
func WithLogc(ctx context.Context, lg cmblog.Logger, opts ...LogcOption) context.Context {
	opt, _ := applyOptions(opts...)

	ctx = context.WithValue(ctx, opt.logName, lg)

	return ctx
}

func LogcFromCtx(ctx context.Context, opts ...LogcOption) cmblog.Logger {
	opt, _ := applyOptions(opts...)

	var logger cmblog.Logger
	if lg, valid := ctx.Value(opt.logName).(cmblog.Logger); valid {
		logger = lg
	} else {
		logger = &dummyLogger{}
	}

	return logger
}

func LogrFromCtx(ctx context.Context) logr.Logger {
	lg, _ := logr.FromContext(ctx)
	return lg
}

func StartupChFromCtx(ctx context.Context) chan<- struct{} {
	val := ctx.Value(CtxKeyStartupCh)
	if val == nil {
		panic("context does not have startup channel set")
	}

	return val.(chan<- struct{})
}

func KubeConfigFromCtx(ctx context.Context) *rest.Config {
	val := ctx.Value(CtxKeyKubeConfig)
	if val == nil {
		panic("context does not have kubeconfig set")
	}

	return val.(*rest.Config)
}

func KubeClientFromCtx(ctx context.Context) *kubernetes.Clientset {
	val := ctx.Value(CtxKeyKubeClientSet)
	if val == nil {
		panic("context does not have kube client set")
	}

	return val.(*kubernetes.Clientset)
}
func AkashClientFromCtx(ctx context.Context) *akashclientset.Clientset {
	val := ctx.Value(CtxKeyAkashClientSet)
	if val == nil {
		panic("context does not have akash client set")
	}

	return val.(*akashclientset.Clientset)
}

func LifecycleFromCtx(ctx context.Context) lifecycle.Lifecycle {
	val := ctx.Value(CtxKeyLifecycle)
	if val == nil {
		panic("context does not have lifecycle set")
	}

	return val.(lifecycle.Lifecycle)
}

func ErrGroupFromCtx(ctx context.Context) *errgroup.Group {
	val := ctx.Value(CtxKeyErrGroup)
	if val == nil {
		panic("context does not have errgroup set")
	}

	return val.(*errgroup.Group)
}

func PubSubFromCtx(ctx context.Context) pubsub.PubSub {
	val := ctx.Value(CtxKeyPubSub)
	if val == nil {
		panic("context does not have pubsub set")
	}

	return val.(pubsub.PubSub)
}

func applyOptions(opts ...LogcOption) (options, error) {
	obj := &options{}
	for _, opt := range opts {
		if err := opt(obj); err != nil {
			return options{}, err
		}
	}

	if obj.logName == "" {
		obj.logName = CtxKeyLogc
	}

	return *obj, nil
}
