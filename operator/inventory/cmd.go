package inventory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/pprof"
	"strings"
	"time"

	"github.com/akash-network/akash-api/go/grpc/gogoreflection"
	inventory "github.com/akash-network/akash-api/go/inventory/v1"
	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/mux"
	rookclientset "github.com/rook/rook/pkg/client/clientset/versioned"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/troian/pubsub"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/protobuf/types/known/emptypb"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/informers"

	kutil "github.com/akash-network/provider/cluster/kube/util"
	providerflags "github.com/akash-network/provider/cmd/provider-services/cmd/flags"
	"github.com/akash-network/provider/operator/common"
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
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			kubecfg := fromctx.MustKubeConfigFromCtx(cmd.Context())

			rc, err := rookclientset.NewForConfig(kubecfg)
			if err != nil {
				return err
			}

			fromctx.CmdSetContextValue(cmd, CtxKeyRookClientSet, rc)

			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			log := fromctx.LogrFromCtx(cmd.Context())
			bus := fromctx.MustPubSubFromCtx(ctx)
			group := fromctx.MustErrGroupFromCtx(ctx)

			var storage []QuerierStorage
			st, err := NewCeph(ctx)
			if err != nil {
				return err
			}
			storage = append(storage, st)

			if st, err = NewRancher(ctx); err != nil {
				return err
			}

			discoveryImage := viper.GetString(FlagDiscoveryImage)
			namespace := viper.GetString(FlagPodNamespace)

			restPort, err := common.DetectPort(ctx, cmd.Flags(), common.FlagRESTPort, "operator-inventory", "rest")
			if err != nil {
				return err
			}

			grpcPort, err := common.DetectPort(ctx, cmd.Flags(), common.FlagGRPCPort, "operator-inventory", "grpc")
			if err != nil {
				return err
			}

			if !kutil.IsInsideKubernetes() {
				log.Info("service is not running as kubernetes pod. detecting discovery image name from flags")
			} else {
				name := viper.GetString(FlagPodName)
				kc := fromctx.MustKubeClientFromCtx(ctx)

				pod, err := kc.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
				if err != nil {
					return err
				}

				for _, container := range pod.Spec.Containers {
					if container.Name == "operator-inventory" {
						discoveryImage = container.Image
						break
					}
				}
			}

			clNodes := newClusterNodes(ctx, discoveryImage, namespace)

			storage = append(storage, st)

			clState := &clusterState{
				ctx:            ctx,
				querierCluster: newQuerierCluster(),
			}

			fromctx.CmdSetContextValue(cmd, CtxKeyStorage, storage)
			fromctx.CmdSetContextValue(cmd, CtxKeyFeatureDiscovery, clNodes)
			fromctx.CmdSetContextValue(cmd, CtxKeyClusterState, QuerierCluster(clState))

			ctx = cmd.Context()

			listenAddress := viper.GetString(common.FlagRESTAddress)
			apiTimeout := viper.GetDuration(FlagAPITimeout)
			queryTimeout := viper.GetDuration(FlagQueryTimeout)

			restEndpoint := fmt.Sprintf("%s:%d", listenAddress, restPort)
			grpcEndpoint := fmt.Sprintf("%s:%d", listenAddress, grpcPort)

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

			gSrc := &grpcServiceServer{
				ctx: ctx,
			}

			inventory.RegisterClusterRPCServer(grpcSrv, gSrc)
			gogoreflection.Register(grpcSrv)

			group.Go(func() error {
				return configWatcher(ctx, viper.GetString(FlagConfig))
			})

			group.Go(func() error {
				return scWatcher(ctx)
			})

			group.Go(func() error {
				return registryLoader(ctx)
			})

			group.Go(clState.run)
			group.Go(clNodes.Wait)

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

			kc := fromctx.MustKubeClientFromCtx(ctx)
			factory := informers.NewSharedInformerFactory(kc, 0)

			InformKubeObjects(ctx,
				bus,
				factory.Core().V1().Namespaces().Informer(),
				topicKubeNS)

			InformKubeObjects(ctx,
				bus,
				factory.Storage().V1().StorageClasses().Informer(),
				topicKubeSC)

			InformKubeObjects(ctx,
				bus,
				factory.Core().V1().PersistentVolumes().Informer(),
				topicKubePV)

			InformKubeObjects(ctx,
				bus,
				factory.Core().V1().Nodes().Informer(),
				topicKubeNodes)

			InformKubeObjects(ctx,
				bus,
				factory.Core().V1().Pods().Informer(),
				topicKubePods)

			fromctx.MustStartupChFromCtx(ctx) <- struct{}{}
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

	cmd.Flags().Duration(FlagAPITimeout, 3*time.Second, "api timeout")
	if err = viper.BindPFlag(FlagAPITimeout, cmd.Flags().Lookup(FlagAPITimeout)); err != nil {
		panic(err)
	}

	cmd.Flags().Duration(FlagQueryTimeout, 2*time.Second, "query timeout")
	if err = viper.BindPFlag(FlagQueryTimeout, cmd.Flags().Lookup(FlagQueryTimeout)); err != nil {
		panic(err)
	}

	cmd.Flags().Uint16(common.FlagRESTPort, 8080, "port to REST api")
	if err = viper.BindPFlag(common.FlagRESTPort, cmd.Flags().Lookup(common.FlagRESTPort)); err != nil {
		panic(err)
	}

	cmd.Flags().Uint16(common.FlagGRPCPort, 8081, "port to GRPC api")
	if err = viper.BindPFlag(common.FlagGRPCPort, cmd.Flags().Lookup(common.FlagGRPCPort)); err != nil {
		panic(err)
	}

	cmd.Flags().String(FlagConfig, "", "inventory configuration flag")
	if err = viper.BindPFlag(FlagConfig, cmd.Flags().Lookup(FlagConfig)); err != nil {
		panic(err)
	}

	cmd.Flags().Duration(FlagRegistryQueryPeriod, 5*time.Minute, "query period for registry changes")
	if err = viper.BindPFlag(FlagRegistryQueryPeriod, cmd.Flags().Lookup(FlagRegistryQueryPeriod)); err != nil {
		panic(err)
	}

	cmd.Flags().String(FlagDiscoveryImage, "ghcr.io/akash-network/provider", "hardware discovery docker image")
	if err = viper.BindPFlag(FlagDiscoveryImage, cmd.Flags().Lookup(FlagDiscoveryImage)); err != nil {
		panic(err)
	}

	cmd.Flags().String(FlagPodNamespace, "akash-services", "namespace for discovery pods")
	if err = viper.BindPFlag(FlagPodNamespace, cmd.Flags().Lookup(FlagPodNamespace)); err != nil {
		panic(err)
	}

	cmd.Flags().String(FlagProviderConfigsURL, defaultProviderConfigsURL, "provider configs server")
	if err := viper.BindPFlag(FlagProviderConfigsURL, cmd.Flags().Lookup(FlagProviderConfigsURL)); err != nil {
		panic(err)
	}

	return cmd
}

func configWatcher(ctx context.Context, file string) error {
	log := fromctx.LogrFromCtx(ctx).WithName("watcher.config")

	defer func() {
		log.Info("stopped")
	}()

	bus, err := fromctx.PubSubFromCtx(ctx)
	if err != nil {
		return err
	}

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

	bus.Pub(config, []string{topicInventoryConfig}, pubsub.WithRetain())

	log.Info("started")

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
			bus.Pub(config, []string{topicInventoryConfig}, pubsub.WithRetain())
		}
	}
}

// this function is piece of sh*t. refactor it!
func registryLoader(ctx context.Context) error {
	log := fromctx.LogrFromCtx(ctx).WithName("watcher.registry")
	bus, err := fromctx.PubSubFromCtx(ctx)
	if err != nil {
		return err
	}

	tlsConfig := http.DefaultTransport.(*http.Transport).TLSClientConfig

	cl := &http.Client{
		Transport: &http.Transport{
			DialTLSContext: func(_ context.Context, network, addr string) (net.Conn, error) {
				return tls.Dial(network, addr, tlsConfig)
			},
		},
	}

	urlGPU := fmt.Sprintf("%s/devices/gpus", strings.TrimSuffix(viper.GetString(FlagProviderConfigsURL), "/"))
	urlPcieDB := viper.GetString(FlagPciDbURL)

	var gpuCurrHash []byte
	var pcidbHash []byte

	gpuIDs := make(RegistryGPUVendors)

	queryGPUs := func() bool {
		res, err := cl.Get(urlGPU)
		if err != nil {
			log.Error(err, "couldn't query inventory registry")
			return false
		}

		defer func() {
			_ = res.Body.Close()
		}()

		if res.StatusCode != http.StatusOK {
			return false
		}

		gpus, err := io.ReadAll(res.Body)
		if err != nil {
			return false
		}

		upstreamHash := sha256.New()
		_, _ = upstreamHash.Write(gpus)
		newHash := upstreamHash.Sum(nil)

		if bytes.Equal(gpuCurrHash, newHash) {
			return false
		}

		_ = json.Unmarshal(gpus, &gpuIDs)

		gpuCurrHash = newHash

		return true
	}

	queryPCI := func() bool {
		res, err := cl.Get(urlPcieDB)
		if err != nil {
			log.Error(err, "couldn't query pci.ids")
			return false
		}

		defer func() {
			_ = res.Body.Close()
		}()

		if res.StatusCode != http.StatusOK {
			return false
		}

		pcie, err := io.ReadAll(res.Body)
		if err != nil {
			return false
		}

		upstreamHash := sha256.New()
		_, _ = upstreamHash.Write(pcie)
		newHash := upstreamHash.Sum(nil)

		if bytes.Equal(pcidbHash, newHash) {
			return false
		}

		pcidbHash = newHash

		return true
	}

	queryGPUs()
	bus.Pub(gpuIDs, []string{topicGPUIDs})

	queryPeriod := viper.GetDuration(FlagRegistryQueryPeriod)
	tmGPU := time.NewTimer(queryPeriod)
	tmPCIe := time.NewTimer(24 * time.Hour)

	for {
		select {
		case <-ctx.Done():
			if !tmGPU.Stop() {
				<-tmGPU.C
			}

			if !tmPCIe.Stop() {
				<-tmPCIe.C
			}

			return ctx.Err()
		case <-tmGPU.C:
			if queryGPUs() {
				bus.Pub(gpuIDs, []string{topicGPUIDs})
			}
			tmGPU.Reset(queryPeriod)
		case <-tmPCIe.C:
			queryPCI()

			tmGPU.Reset(24 * time.Hour)
		}
	}
}

func scWatcher(ctx context.Context) error {
	log := fromctx.LogrFromCtx(ctx).WithName("watcher.storageclasses")

	defer func() {
		log.Info("stopped")
	}()

	bus, err := fromctx.PubSubFromCtx(ctx)
	if err != nil {
		return err
	}

	scch := bus.Sub(topicKubeSC)

	sc := make(storageClasses)

	bus.Pub(sc.copy(), []string{topicStorageClasses}, pubsub.WithRetain())

	log.Info("started")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case rEvt := <-scch:
			evt, valid := rEvt.(watch.Event)
			if !valid {
				continue
			}

			switch obj := evt.Object.(type) {
			case *storagev1.StorageClass:
				switch evt.Type {
				case watch.Added:
					sc[obj.Name] = obj.DeepCopy()
				case watch.Deleted:
					delete(sc, obj.Name)
				}
			}

			bus.Pub(sc.copy(), []string{topicStorageClasses}, pubsub.WithRetain())
		}
	}
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

	debugRouter := mRouter.PathPrefix("/debug").Subrouter()
	debugRouter.HandleFunc("/pprof/", pprof.Index)
	debugRouter.HandleFunc("/pprof/cmdline", pprof.Cmdline)
	debugRouter.HandleFunc("/pprof/profile", pprof.Profile)
	debugRouter.HandleFunc("/pprof/symbol", pprof.Symbol)
	debugRouter.HandleFunc("/pprof/trace", pprof.Trace)

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
	bus, err := fromctx.PubSubFromCtx(gm.ctx)
	if err != nil {
		return err
	}

	subch := bus.Sub(topicInventoryCluster)

	defer func() {
		bus.Unsub(subch, topicInventoryCluster)
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
