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
	"pkg.akt.dev/go/util/ctxlog"

	providerflags "github.com/akash-network/provider/cmd/provider-services/cmd/flags"
	akashclientset "github.com/akash-network/provider/pkg/client/clientset/versioned"
	"github.com/akash-network/provider/tools/certissuer"
	"github.com/akash-network/provider/tools/pconfig"
	"github.com/akash-network/provider/types"
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
	CtxKeyLogc               = ctxlog.CtxKeyLog
	CtxKeyStartupCh          = Key("startup-ch")
	CtxKeyInventoryUnderTest = Key("inventory-under-test")
	CtxKeyPersistentConfig   = Key("persistent-config")
	CtxKeyCertIssuer         = Key("cert-issuer")
	CtxKeyAccountQuerier     = Key("account-querier")
)

var (
	ErrNotFound         = errors.New("fromctx: not found")
	ErrValueInvalidType = errors.New("fromctx: invalid type")
)

func CmdSetContextValue(cmd *cobra.Command, key, val interface{}) {
	cmd.SetContext(context.WithValue(cmd.Context(), key, val))
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

func PersistentConfigReaderFromCtx(ctx context.Context) (pconfig.StorageR, error) {
	val := ctx.Value(CtxKeyPersistentConfig)
	if val == nil {
		return nil, fmt.Errorf("%w: context does not have persistent config reader set", ErrNotFound)
	}

	res, valid := val.(pconfig.Storage)
	if !valid {
		return nil, ErrValueInvalidType
	}

	return res, nil
}

func PersistentConfigWriterFromCtx(ctx context.Context) (pconfig.StorageW, error) {
	val := ctx.Value(CtxKeyPersistentConfig)
	if val == nil {
		return nil, fmt.Errorf("%w: context does not have persistent config writer set", ErrNotFound)
	}

	res, valid := val.(pconfig.Storage)
	if !valid {
		return nil, ErrValueInvalidType
	}

	return res, nil
}

func PersistentConfigFromCtx(ctx context.Context) (pconfig.Storage, error) {
	val := ctx.Value(CtxKeyPersistentConfig)
	if val == nil {
		return nil, fmt.Errorf("%w: context does not have persistent config set", ErrNotFound)
	}

	res, valid := val.(pconfig.Storage)
	if !valid {
		return nil, ErrValueInvalidType
	}

	return res, nil
}

func AccountQuerierFromCtx(ctx context.Context) (types.AccountQuerier, error) {
	val := ctx.Value(CtxKeyAccountQuerier)
	if val == nil {
		return nil, fmt.Errorf("%w: context does not have account querier set", ErrNotFound)
	}

	res, valid := val.(types.AccountQuerier)
	if !valid {
		return nil, ErrValueInvalidType
	}

	return res, nil
}

func MustAccountQuerierFromCtx(ctx context.Context) types.AccountQuerier {
	v, err := AccountQuerierFromCtx(ctx)
	if err != nil {
		panic(err.Error())
	}
	return v
}

func CertIssuerFromCtx(ctx context.Context) (certissuer.CertIssuer, error) {
	val := ctx.Value(CtxKeyCertIssuer)
	if val == nil {
		return nil, fmt.Errorf("%w: context does not have cert issuer set", ErrNotFound)
	}

	res, valid := val.(certissuer.CertIssuer)
	if !valid {
		return nil, ErrValueInvalidType
	}

	return res, nil
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

func ApplyToContext(ctx context.Context, config map[interface{}]interface{}) context.Context {
	for k, v := range config {
		ctx = context.WithValue(ctx, k, v)
	}

	return ctx
}
