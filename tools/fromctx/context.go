package fromctx

import (
	"context"
	"errors"
	"fmt"

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
	CtxKeyKubeConfig         = Key(providerflags.FlagKubeConfig)
	CtxKeyKubeRESTClient     = Key("kube-restclient")
	CtxKeyKubeClientSet      = Key("kube-clientset")
	CtxKeyAkashClientSet     = Key("akash-clientset")
	CtxKeyPubSub             = Key("pubsub")
	CtxKeyLifecycle          = Key("lifecycle")
	CtxKeyErrGroup           = Key("errgroup")
	CtxKeyLogc               = Key("logc")
	CtxKeyStartupCh          = Key("startup-ch")
	CtxKeyInventoryUnderTest = Key("inventory-under-test")
)

var (
	ErrNotFound         = errors.New("fromctx: not found")
	ErrValueInvalidType = errors.New("fromctx: invalid type")
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

func StartupChFromCtx(ctx context.Context) (chan<- struct{}, error) {
	val := ctx.Value(CtxKeyStartupCh)
	if val == nil {
		return nil, fmt.Errorf("%w: context does not have startup channel set", ErrNotFound)
	}

	res, valid := val.(chan<- struct{})
	if !valid {
		return nil, ErrValueInvalidType
	}

	return res, nil
}

func MustStartupChFromCtx(ctx context.Context) chan<- struct{} {
	val, err := StartupChFromCtx(ctx)
	if err != nil {
		panic(err.Error())
	}

	return val
}

func KubeRESTClientFromCtx(ctx context.Context) (*rest.RESTClient, error) {
	val := ctx.Value(CtxKeyKubeRESTClient)
	if val == nil {
		return nil, fmt.Errorf("%w: context does not have kube rest client set", ErrNotFound)
	}

	res, valid := val.(*rest.RESTClient)
	if !valid {
		return nil, ErrValueInvalidType
	}

	return res, nil
}

func MustKubeRESTClientFromCtx(ctx context.Context) *rest.RESTClient {
	val, err := KubeRESTClientFromCtx(ctx)
	if err != nil {
		panic(err.Error())
	}

	return val
}

func KubeConfigFromCtx(ctx context.Context) (*rest.Config, error) {
	val := ctx.Value(CtxKeyKubeConfig)
	if val == nil {
		return nil, fmt.Errorf("%w: context does not have kubeconfig set", ErrNotFound)
	}

	res, valid := val.(*rest.Config)
	if !valid {
		return nil, ErrValueInvalidType
	}

	return res, nil
}

func MustKubeConfigFromCtx(ctx context.Context) *rest.Config {
	val, err := KubeConfigFromCtx(ctx)
	if err != nil {
		panic(err.Error())
	}

	return val
}

func KubeClientFromCtx(ctx context.Context) (kubernetes.Interface, error) {
	val := ctx.Value(CtxKeyKubeClientSet)
	if val == nil {
		return nil, fmt.Errorf("%w: context does not have kube client set", ErrNotFound)
	}

	res, valid := val.(kubernetes.Interface)
	if !valid {
		return nil, ErrValueInvalidType
	}

	return res, nil
}

func MustKubeClientFromCtx(ctx context.Context) kubernetes.Interface {
	val, err := KubeClientFromCtx(ctx)
	if err != nil {
		panic(err.Error())
	}

	return val
}

func AkashClientFromCtx(ctx context.Context) (akashclientset.Interface, error) {
	val := ctx.Value(CtxKeyAkashClientSet)
	if val == nil {
		return nil, fmt.Errorf("%w: context does not have akash client set", ErrNotFound)
	}

	res, valid := val.(akashclientset.Interface)
	if !valid {
		return nil, ErrValueInvalidType
	}

	return res, nil
}

func MustAkashClientFromCtx(ctx context.Context) akashclientset.Interface {
	val, err := AkashClientFromCtx(ctx)
	if err != nil {
		panic(err.Error())
	}

	return val
}

func LifecycleFromCtx(ctx context.Context) (lifecycle.Lifecycle, error) {
	val := ctx.Value(CtxKeyLifecycle)
	if val == nil {
		return nil, fmt.Errorf("%w: context does not have lifecycle set", ErrNotFound)
	}

	res, valid := val.(lifecycle.Lifecycle)
	if !valid {
		return nil, ErrValueInvalidType
	}

	return res, nil
}

func MustLifecycleFromCtx(ctx context.Context) lifecycle.Lifecycle {
	val, err := LifecycleFromCtx(ctx)
	if err != nil {
		panic(err.Error())
	}

	return val
}

func ErrGroupFromCtx(ctx context.Context) (*errgroup.Group, error) {
	val := ctx.Value(CtxKeyErrGroup)
	if val == nil {
		return nil, fmt.Errorf("%w: context does not have errgroup set", ErrNotFound)
	}

	res, valid := val.(*errgroup.Group)
	if !valid {
		return nil, ErrValueInvalidType
	}

	return res, nil
}

func MustErrGroupFromCtx(ctx context.Context) *errgroup.Group {
	val, err := ErrGroupFromCtx(ctx)
	if err != nil {
		panic(err.Error())
	}

	return val
}

func PubSubFromCtx(ctx context.Context) (pubsub.PubSub, error) {
	val := ctx.Value(CtxKeyPubSub)
	if val == nil {
		return nil, fmt.Errorf("%w: context does not have pubsub set", ErrNotFound)
	}

	res, valid := val.(pubsub.PubSub)
	if !valid {
		return nil, ErrValueInvalidType
	}

	return res, nil
}

func MustPubSubFromCtx(ctx context.Context) pubsub.PubSub {
	val, err := PubSubFromCtx(ctx)
	if err != nil {
		panic(err.Error())
	}

	return val
}

func IsInventoryUnderTestFromCtx(ctx context.Context) bool {
	val := ctx.Value(CtxKeyInventoryUnderTest)
	if val == nil {
		return false
	}

	if v, valid := val.(bool); valid {
		return v
	}

	return false
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

func ApplyToContext(ctx context.Context, config map[interface{}]interface{}) context.Context {
	for k, v := range config {
		ctx = context.WithValue(ctx, k, v)
	}

	return ctx
}
