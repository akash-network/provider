package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/gorilla/mux"
	rookclientset "github.com/rook/rook/pkg/client/clientset/versioned"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/troian/pubsub"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
	"google.golang.org/protobuf/types/known/emptypb"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"

	inventory "github.com/akash-network/akash-api/go/inventory/v1"

	"github.com/akash-network/provider/cluster/kube/clientcommon"
	providerflags "github.com/akash-network/provider/cmd/provider-services/cmd/flags"
	cmdutil "github.com/akash-network/provider/cmd/provider-services/cmd/util"
	akashclientset "github.com/akash-network/provider/pkg/client/clientset/versioned"
	"github.com/akash-network/provider/tools/fromctx"
)

type serviceRouter struct {
	*mux.Router
	queryTimeout time.Duration
}

type grpcServiceServer struct {
	inventory.ClusterRPCServer
	ctx context.Context
}

func Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "inventory",
		Short:        "kubernetes operator interfacing inventory",
		SilenceUsage: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			zconf := zap.NewDevelopmentConfig()
			zconf.DisableCaller = true
			zconf.DisableStacktrace = true
			zconf.EncoderConfig.EncodeTime = func(time.Time, zapcore.PrimitiveArrayEncoder) {}

			zapLog, _ := zconf.Build()

			group, ctx := errgroup.WithContext(cmd.Context())

			cmd.SetContext(logr.NewContext(ctx, zapr.NewLogger(zapLog)))

			if err := loadKubeConfig(cmd); err != nil {
				return err
			}

			kubecfg := fromctx.KubeConfigFromCtx(cmd.Context())

			kc, err := kubernetes.NewForConfig(kubecfg)
			if err != nil {
				return err
			}

			startupch := make(chan struct{}, 1)
			pctx, pcancel := context.WithCancel(context.Background())

			fromctx.CmdSetContextValue(cmd, fromctx.CtxKeyStartupCh, (chan<- struct{})(startupch))
			fromctx.CmdSetContextValue(cmd, fromctx.CtxKeyKubeConfig, kubecfg)
			fromctx.CmdSetContextValue(cmd, fromctx.CtxKeyKubeClientSet, kc)
			fromctx.CmdSetContextValue(cmd, fromctx.CtxKeyErrGroup, group)
			fromctx.CmdSetContextValue(cmd, fromctx.CtxKeyPubSub, pubsub.New(pctx, 1000))

			go func() {
				defer pcancel()

				select {
				case <-ctx.Done():
					return
				case <-startupch:
				}

				_ = group.Wait()
			}()

			return nil
		},
		PreRunE: func(cmd *cobra.Command, args []string) error {
			kubecfg := fromctx.KubeConfigFromCtx(cmd.Context())

			rc, err := rookclientset.NewForConfig(kubecfg)
			if err != nil {
				return err
			}

			ac, err := akashclientset.NewForConfig(kubecfg)
			if err != nil {
				return err
			}

			fromctx.CmdSetContextValue(cmd, CtxKeyRookClientSet, rc)
			fromctx.CmdSetContextValue(cmd, fromctx.CtxKeyAkashClientSet, ac)

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			bus := fromctx.PubSubFromCtx(ctx)
			group := fromctx.ErrGroupFromCtx(ctx)

			var storage []QuerierStorage
			st, err := NewCeph(ctx)
			if err != nil {
				return err
			}
			storage = append(storage, st)

			if st, err = NewRancher(ctx); err != nil {
				return err
			}

			fd := newFeatureDiscovery(ctx)

			storage = append(storage, st)

			clState := &clusterState{
				ctx:            ctx,
				querierCluster: newQuerierCluster(),
			}

			fromctx.CmdSetContextValue(cmd, CtxKeyStorage, storage)
			fromctx.CmdSetContextValue(cmd, CtxKeyFeatureDiscovery, fd)
			fromctx.CmdSetContextValue(cmd, CtxKeyClusterState, QuerierCluster(clState))

			ctx = cmd.Context()

			restPort := viper.GetUint16(FlagRESTPort)
			grpcPort := viper.GetUint16(FlagGRPCPort)

			apiTimeout := viper.GetDuration(FlagAPITimeout)
			queryTimeout := viper.GetDuration(FlagQueryTimeout)
			restEndpoint := fmt.Sprintf(":%d", restPort)
			grpcEndpoint := fmt.Sprintf(":%d", grpcPort)

			log := fromctx.LogrFromCtx(ctx)

			restSrv := &http.Server{
				Addr:    restEndpoint,
				Handler: newServiceRouter(apiTimeout, queryTimeout),
				BaseContext: func(_ net.Listener) context.Context {
					return ctx
				},
				ReadHeaderTimeout: 5 * time.Second,
				ReadTimeout:       60 * time.Second,
			}

			grpcSrv := grpc.NewServer(grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
				MinTime:             30 * time.Second,
				PermitWithoutStream: false,
			}))

			inventory.RegisterClusterRPCServer(grpcSrv, &grpcServiceServer{
				ctx: ctx,
			})

			reflection.Register(grpcSrv)

			group.Go(func() error {
				return configWatcher(ctx, viper.GetString(FlagConfig))
			})

			group.Go(clState.run)
			group.Go(fd.Wait)
			group.Go(func() error {
				log.Info(fmt.Sprintf("rest listening on \"%s\"", restEndpoint))

				return restSrv.ListenAndServe()
			})

			group.Go(func() error {
				grpcLis, err := net.Listen("tcp", grpcEndpoint)
				if err != nil {
					return err
				}

				log.Info(fmt.Sprintf("grpc listening on \"%s\"", grpcEndpoint))

				return grpcSrv.Serve(grpcLis)
			})

			group.Go(func() error {
				<-ctx.Done()
				err := restSrv.Shutdown(context.Background())

				grpcSrv.GracefulStop()

				if err == nil {
					err = ctx.Err()
				}

				return err
			})

			kc := fromctx.KubeClientFromCtx(ctx)
			factory := informers.NewSharedInformerFactory(kc, 0)

			InformKubeObjects(ctx,
				bus,
				factory.Core().V1().Namespaces().Informer(),
				"ns")

			InformKubeObjects(ctx,
				bus,
				factory.Storage().V1().StorageClasses().Informer(),
				"sc")

			InformKubeObjects(ctx,
				bus,
				factory.Core().V1().PersistentVolumes().Informer(),
				"pv")

			InformKubeObjects(ctx,
				bus,
				factory.Core().V1().Nodes().Informer(),
				"nodes")

			fromctx.StartupChFromCtx(ctx) <- struct{}{}
			err = group.Wait()

			if !errors.Is(err, context.Canceled) {
				return err
			}

			return nil
		},
	}

	err := providerflags.AddKubeConfigPathFlag(cmd)
	if err != nil {
		panic(err)
	}

	cmd.PersistentFlags().Duration(FlagAPITimeout, 3*time.Second, "api timeout")
	if err = viper.BindPFlag(FlagAPITimeout, cmd.PersistentFlags().Lookup(FlagAPITimeout)); err != nil {
		panic(err)
	}

	cmd.PersistentFlags().Duration(FlagQueryTimeout, 2*time.Second, "query timeout")
	if err = viper.BindPFlag(FlagQueryTimeout, cmd.PersistentFlags().Lookup(FlagQueryTimeout)); err != nil {
		panic(err)
	}

	cmd.PersistentFlags().Uint16(FlagRESTPort, 8080, "port to REST api")
	if err = viper.BindPFlag(FlagRESTPort, cmd.PersistentFlags().Lookup(FlagRESTPort)); err != nil {
		panic(err)
	}

	cmd.PersistentFlags().Uint16(FlagGRPCPort, 8081, "port to GRPC api")
	if err = viper.BindPFlag(FlagGRPCPort, cmd.PersistentFlags().Lookup(FlagGRPCPort)); err != nil {
		panic(err)
	}

	cmd.Flags().String(FlagConfig, "", "inventory configuration flag")
	if err = viper.BindPFlag(FlagConfig, cmd.Flags().Lookup(FlagConfig)); err != nil {
		panic(err)
	}

	cmd.AddCommand(cmdFeatureDiscoveryNode())

	return cmd
}

func loadKubeConfig(c *cobra.Command) error {
	configPath, _ := c.Flags().GetString(providerflags.FlagKubeConfig)

	config, err := clientcommon.OpenKubeConfig(configPath, cmdutil.OpenLogger().With("cmp", "provider"))
	if err != nil {
		return err
	}

	fromctx.CmdSetContextValue(c, fromctx.CtxKeyKubeConfig, config)

	return nil
}

func newServiceRouter(apiTimeout, queryTimeout time.Duration) *serviceRouter {
	mRouter := mux.NewRouter()
	rt := &serviceRouter{
		Router:       mRouter,
		queryTimeout: queryTimeout,
	}

	mRouter.Use(func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rCtx, cancel := context.WithTimeout(r.Context(), apiTimeout)
			defer cancel()

			h.ServeHTTP(w, r.WithContext(rCtx))
		})
	})

	metricsRouter := mRouter.PathPrefix("/metrics").Subrouter()
	inventoryRouter := mRouter.PathPrefix("/v1").Subrouter()
	inventoryRouter.HandleFunc("/inventory", rt.inventoryHandler)

	metricsRouter.HandleFunc("/health", rt.healthHandler).GetHandler()
	metricsRouter.HandleFunc("/ready", rt.readyHandler)

	return rt
}

func (rt *serviceRouter) healthHandler(w http.ResponseWriter, req *http.Request) {
	var err error

	defer func() {
		code := http.StatusOK

		if err != nil {
			if errors.Is(err, ErrMetricsUnsupportedRequest) {
				code = http.StatusBadRequest
			} else {
				code = http.StatusInternalServerError
			}
		}

		w.WriteHeader(code)
	}()

	if req.Method != "" && req.Method != http.MethodGet {
		err = ErrMetricsUnsupportedRequest
		return
	}

	return
}

func (rt *serviceRouter) readyHandler(w http.ResponseWriter, req *http.Request) {
	var err error

	defer func() {
		code := http.StatusOK

		if err != nil {
			if errors.Is(err, ErrMetricsUnsupportedRequest) {
				code = http.StatusBadRequest
			} else {
				code = http.StatusInternalServerError
			}
		}

		w.WriteHeader(code)
	}()

	if req.Method != "" && req.Method != http.MethodGet {
		err = ErrMetricsUnsupportedRequest
		return
	}

	return
}

func (rt *serviceRouter) inventoryHandler(w http.ResponseWriter, req *http.Request) {
	state := ClusterStateFromCtx(req.Context())

	resp, err := state.Query(req.Context())

	var data []byte

	defer func() {
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}

		if len(data) > 0 {
			_, _ = w.Write(data)
		}
	}()

	if err != nil {
		return
	}

	if req.URL.Query().Has("pretty") {
		data, err = json.MarshalIndent(&resp, "", "  ")
	} else {
		data, err = json.Marshal(&resp)
	}

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		data = []byte(err.Error())
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
}

func (gm *grpcServiceServer) QueryCluster(ctx context.Context, _ *emptypb.Empty) (*inventory.Cluster, error) {
	clq := ClusterStateFromCtx(gm.ctx)

	res, err := clq.Query(ctx)
	if err != nil {
		return nil, err
	}

	return &res, nil
}

func (gm *grpcServiceServer) StreamCluster(_ *emptypb.Empty, stream inventory.ClusterRPC_StreamClusterServer) error {
	bus := fromctx.PubSubFromCtx(gm.ctx)

	subch := bus.Sub(topicClusterState)

	defer func() {
		bus.Unsub(subch, topicClusterState)
	}()

loop:
	for {
		select {
		case <-gm.ctx.Done():
			return gm.ctx.Err()
		case <-stream.Context().Done():
			return stream.Context().Err()
		case msg, ok := <-subch:
			if !ok {
				continue loop
			}
			val := msg.(inventory.Cluster)
			if err := stream.Send(&val); err != nil {
				return err
			}
		}
	}
}

func configWatcher(ctx context.Context, file string) error {
	config, err := loadConfig(file, false)
	if err != nil {
		return err
	}

	var watcher *fsnotify.Watcher
	var evtch chan fsnotify.Event

	if strings.HasSuffix(file, "yaml") {
		watcher, err = fsnotify.NewWatcher()
		if err != nil {
			return err
		}
	}

	defer func() {
		if watcher != nil {
			_ = watcher.Close()
		}
	}()

	if watcher != nil {
		if err = watcher.Add(file); err != nil {
			return err
		}

		evtch = watcher.Events
	}

	bus := fromctx.PubSubFromCtx(ctx)

	bus.Pub(config, []string{"config"}, pubsub.WithRetain())

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case evt := <-evtch:
			if evt.Has(fsnotify.Create) || evt.Has(fsnotify.Write) {
				config, _ = loadConfig(evt.Name, true)
			} else if evt.Has(fsnotify.Remove) {
				config, _ = loadConfig("", true)
			}
			bus.Pub(config, []string{"config"}, pubsub.WithRetain())
		}
	}
}
