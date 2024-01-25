package inventory

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	"github.com/gorilla/mux"
	"github.com/jaypipes/ghw/pkg/cpu"
	"github.com/jaypipes/ghw/pkg/gpu"
	"github.com/jaypipes/ghw/pkg/memory"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/troian/pubsub"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
	"google.golang.org/protobuf/types/known/emptypb"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/watch"

	v1 "github.com/akash-network/akash-api/go/inventory/v1"

	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/tools/fromctx"
)

const (
	fdContainerName         = "inventory-node"
	fdContainerRESTPortName = "api"
	fdContainerGRPCPortName = "grpc"
	topicNode               = "node"
	topicNodeState          = "node-state"
	topicNodes              = "nodes"
	topicStorage            = "storage"
	topicConfig             = "config"
	topicClusterState       = "cluster-state"
)

type gpuDevice struct {
	Name       string `json:"name"`
	Interface  string `json:"interface"`
	MemorySize string `json:"memory_size"`
}

type gpuDevices map[string]gpuDevice

type gpuVendor struct {
	Name    string     `json:"name"`
	Devices gpuDevices `json:"devices"`
}

type gpuVendors map[string]gpuVendor

type dpReqType int

const (
	dpReqCPU dpReqType = iota
	dpReqGPU
	dpReqMem
)

type dpReadResp struct {
	data interface{}
	err  error
}
type dpReadReq struct {
	op   dpReqType
	resp chan<- dpReadResp
}

type debuggerPod struct {
	ctx    context.Context
	readch chan dpReadReq
}

type nodeRouter struct {
	*mux.Router
	queryTimeout time.Duration
}

type grpcNodeServer struct {
	v1.NodeRPCServer
	ctx   context.Context
	log   logr.Logger
	sub   pubsub.Subscriber
	reqch chan<- chan<- v1.Node
}

type fdNodeServer struct {
	ctx      context.Context
	log      logr.Logger
	reqch    <-chan chan<- v1.Node
	pub      pubsub.Publisher
	nodeName string
}

var (
	supportedGPUs = gpuVendors{}

	//go:embed gpu-info.json
	gpuDevs embed.FS
)

func init() {
	f, err := gpuDevs.Open("gpu-info.json")
	if err != nil {
		panic(err)
	}
	// close pci.ids file when done
	defer func() {
		_ = f.Close()
	}()

	data, err := io.ReadAll(f)
	if err != nil {
		panic(err)
	}

	err = json.Unmarshal(data, &supportedGPUs)
	if err != nil {
		panic(err)
	}
}

func cmdFeatureDiscoveryNode() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "node",
		Short:        "feature discovery daemon-set node",
		Args:         cobra.ExactArgs(0),
		SilenceUsage: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			kubecfg := fromctx.KubeConfigFromCtx(cmd.Context())

			var hw hwInfo

			log := fromctx.LogrFromCtx(cmd.Context())

			if kubecfg.BearerTokenFile != "/var/run/secrets/kubernetes.io/serviceaccount/token" {
				log.Info("service is not running as kubernetes pod. starting debugger pod")

				dp := &debuggerPod{
					ctx:    cmd.Context(),
					readch: make(chan dpReadReq, 1),
				}

				group := fromctx.ErrGroupFromCtx(cmd.Context())

				startch := make(chan struct{})

				group.Go(func() error {
					return dp.run(startch)
				})

				ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)

				select {
				case <-ctx.Done():
					if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
						return ctx.Err()
					}
				case <-startch:
					cancel()
				}

				hw = dp
			} else {
				hw = &localHwReader{}
			}

			fromctx.CmdSetContextValue(cmd, CtxKeyHwInfo, hw)

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			log := fromctx.LogrFromCtx(ctx)

			log.Info("starting k8s node features discovery")

			var err error

			podName := viper.GetString(FlagPodName)
			nodeName := viper.GetString(FlagNodeName)

			restPort := viper.GetUint16(FlagRESTPort)
			grpcPort := viper.GetUint16(FlagGRPCPort)

			apiTimeout := viper.GetDuration(FlagAPITimeout)
			queryTimeout := viper.GetDuration(FlagQueryTimeout)

			kc := fromctx.KubeClientFromCtx(ctx)

			if grpcPort == 0 {
				// this is dirty hack to discover exposed api port if this service runs within kubernetes
				podInfo, err := kc.CoreV1().Pods(daemonSetNamespace).Get(ctx, podName, metav1.GetOptions{})
				if err != nil {
					return err
				}

				for _, container := range podInfo.Spec.Containers {
					if container.Name == fdContainerName {
						for _, port := range container.Ports {
							if port.Name == fdContainerGRPCPortName {
								grpcPort = uint16(port.ContainerPort)
							}
						}
					}
				}

				if grpcPort == 0 {
					return fmt.Errorf("unable to detect pod's grpc port") // nolint: goerr113
				}
			}

			if restPort == 0 {
				// this is dirty hack to discover exposed api port if this service runs within kubernetes
				podInfo, err := kc.CoreV1().Pods(daemonSetNamespace).Get(ctx, podName, metav1.GetOptions{})
				if err != nil {
					return err
				}

				for _, container := range podInfo.Spec.Containers {
					if container.Name == fdContainerName {
						for _, port := range container.Ports {
							if port.Name == fdContainerRESTPortName {
								restPort = uint16(port.ContainerPort)
							}
						}
					}
				}

				if grpcPort == 0 {
					return fmt.Errorf("unable to detect pod's grpc port") // nolint: goerr113
				}
			}

			restEndpoint := fmt.Sprintf(":%d", restPort)
			grpcEndpoint := fmt.Sprintf(":%d", grpcPort)

			restSrv := &http.Server{
				Addr:    restEndpoint,
				Handler: newNodeRouter(apiTimeout, queryTimeout),
				BaseContext: func(_ net.Listener) context.Context {
					return ctx
				},
				ReadHeaderTimeout: 5 * time.Second,
				ReadTimeout:       60 * time.Second,
			}

			bus := fromctx.PubSubFromCtx(cmd.Context())

			grpcSrv := grpc.NewServer(grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
				MinTime:             30 * time.Second,
				PermitWithoutStream: false,
			}))

			reqch := make(chan chan<- v1.Node, 1)

			v1.RegisterNodeRPCServer(grpcSrv, &grpcNodeServer{
				ctx:   ctx,
				log:   log.WithName("msg-srv"),
				sub:   bus,
				reqch: reqch,
			})

			reflection.Register(grpcSrv)

			group := fromctx.ErrGroupFromCtx(ctx)

			fdns := &fdNodeServer{
				ctx:      ctx,
				log:      log.WithName("watcher"),
				reqch:    reqch,
				pub:      bus,
				nodeName: nodeName,
			}

			startch := make(chan struct{}, 1)
			group.Go(func() error {
				defer func() {
					log.Info("node discovery stopped")
				}()
				return fdns.run(startch)
			})

			select {
			case <-startch:
				group.Go(func() error {
					defer func() {
						log.Info("grpc server stopped")
					}()

					log.Info(fmt.Sprintf("grpc listening on \"%s\"", grpcEndpoint))

					lis, err := net.Listen("tcp", grpcEndpoint)
					if err != nil {
						return err
					}

					return grpcSrv.Serve(lis)
				})
			case <-ctx.Done():
				return ctx.Err()
			}

			group.Go(func() error {
				log.Info(fmt.Sprintf("rest listening on \"%s\"", restEndpoint))

				return restSrv.ListenAndServe()
			})

			group.Go(func() error {
				<-ctx.Done()
				log.Info("received shutdown signal")

				err := restSrv.Shutdown(context.Background())

				grpcSrv.GracefulStop()

				if err == nil {
					err = ctx.Err()
				}

				return err
			})

			fromctx.StartupChFromCtx(ctx) <- struct{}{}
			err = group.Wait()

			if !errors.Is(err, context.Canceled) {
				return err
			}

			return nil
		},
	}

	cmd.Flags().String(FlagPodName, "", "instance name")
	if err := viper.BindPFlag(FlagPodName, cmd.Flags().Lookup(FlagPodName)); err != nil {
		panic(err)
	}

	cmd.Flags().String(FlagNodeName, "", "node name")
	if err := viper.BindPFlag(FlagNodeName, cmd.Flags().Lookup(FlagNodeName)); err != nil {
		panic(err)
	}

	return cmd
}

func newNodeRouter(apiTimeout, queryTimeout time.Duration) *nodeRouter {
	mRouter := mux.NewRouter()
	rt := &nodeRouter{
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
	metricsRouter.HandleFunc("/health", rt.healthHandler).GetHandler()
	metricsRouter.HandleFunc("/ready", rt.readyHandler)

	return rt
}

func (rt *nodeRouter) healthHandler(w http.ResponseWriter, req *http.Request) {
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

func (rt *nodeRouter) readyHandler(w http.ResponseWriter, req *http.Request) {
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

func (nd *fdNodeServer) run(startch chan<- struct{}) error {
	kc := fromctx.KubeClientFromCtx(nd.ctx)

	nodeWatch, err := kc.CoreV1().Nodes().Watch(nd.ctx, metav1.ListOptions{
		LabelSelector: builder.AkashManagedLabelName + "=true",
		FieldSelector: fields.OneTermEqualSelector(metav1.ObjectNameField, nd.nodeName).String(),
	})
	if err != nil {
		nd.log.Error(err, fmt.Sprintf("unable to start node watcher for \"%s\"", nd.nodeName))
		return err
	}

	defer nodeWatch.Stop()

	podsWatch, err := kc.CoreV1().Pods(corev1.NamespaceAll).Watch(nd.ctx, metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("spec.nodeName", nd.nodeName).String(),
	})
	if err != nil {
		nd.log.Error(err, "unable to fetch pods")
		return err
	}

	defer podsWatch.Stop()

	node, initPods, err := initNodeInfo(nd.ctx, nd.nodeName)
	if err != nil {
		nd.log.Error(err, "unable to init node info")
		return err
	}

	select {
	case <-nd.ctx.Done():
		return nd.ctx.Err()
	case startch <- struct{}{}:
	}

	signalch := make(chan struct{}, 1)
	signalch <- struct{}{}

	trySignal := func() {
		select {
		case signalch <- struct{}{}:
		default:
		}
	}

	trySignal()

	for {
		select {
		case <-nd.ctx.Done():
			return nd.ctx.Err()
		case <-signalch:
			nd.pub.Pub(node.Dup(), []string{topicNode}, pubsub.WithRetain())
		case req := <-nd.reqch:
			req <- node.Dup()
		case res := <-nodeWatch.ResultChan():
			obj := res.Object.(*corev1.Node)
			switch res.Type {
			case watch.Added:
				fallthrough
			case watch.Modified:
				caps, _ := parseNodeCapabilities(obj.Annotations)
				switch obj := caps.Capabilities.(type) {
				case *CapabilitiesV1:
					node.Capabilities.StorageClasses = obj.StorageClasses
				default:
				}
			}
			trySignal()
		case res := <-podsWatch.ResultChan():
			obj := res.Object.(*corev1.Pod)
			switch res.Type {
			case watch.Added:
				if _, exists := initPods[obj.Name]; exists {
					delete(initPods, obj.Name)
				} else {
					for _, container := range obj.Spec.Containers {
						addAllocatedResources(&node, container.Resources.Requests)
					}
				}
			case watch.Deleted:
				delete(initPods, obj.Name)

				for _, container := range obj.Spec.Containers {
					subAllocatedResources(&node, container.Resources.Requests)
				}
			}

			trySignal()
		}
	}
}

func addAllocatedResources(node *v1.Node, rl corev1.ResourceList) {
	for name, quantity := range rl {
		switch name {
		case corev1.ResourceCPU:
			node.Resources.CPU.Quantity.Allocated.Add(quantity)
		case corev1.ResourceMemory:
			node.Resources.Memory.Quantity.Allocated.Add(quantity)
		case corev1.ResourceEphemeralStorage:
			node.Resources.EphemeralStorage.Allocated.Add(quantity)
		case builder.ResourceGPUNvidia:
			fallthrough
		case builder.ResourceGPUAMD:
			node.Resources.GPU.Quantity.Allocated.Add(quantity)
		}
	}
}

func subAllocatedResources(node *v1.Node, rl corev1.ResourceList) {
	for name, quantity := range rl {
		switch name {
		case corev1.ResourceCPU:
			node.Resources.CPU.Quantity.Allocated.Sub(quantity)
		case corev1.ResourceMemory:
			node.Resources.Memory.Quantity.Allocated.Sub(quantity)
		case corev1.ResourceEphemeralStorage:
			node.Resources.EphemeralStorage.Allocated.Sub(quantity)
		case builder.ResourceGPUNvidia:
			fallthrough
		case builder.ResourceGPUAMD:
			node.Resources.GPU.Quantity.Allocated.Sub(quantity)
		}
	}
}

func initNodeInfo(ctx context.Context, name string) (v1.Node, map[string]corev1.Pod, error) {
	kc := fromctx.KubeClientFromCtx(ctx)

	cpuInfo, err := parseCPUInfo(ctx)
	if err != nil {
		return v1.Node{}, nil, err
	}

	gpuInfo, err := parseGPUInfo(ctx)
	if err != nil {
		log := fromctx.LogrFromCtx(ctx)
		log.Error(err, "couldn't pull GPU info")
		// return v1.Node{}, nil, err
	}

	knode, err := kc.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return v1.Node{}, nil, fmt.Errorf("%w: error fetching node %s", err, name)
	}

	caps, err := parseNodeCapabilities(knode.Annotations)
	if err != nil {
		return v1.Node{}, nil, fmt.Errorf("%w: parsing capabilities for node%s", err, name)
	}

	res := v1.Node{
		Name: knode.Name,
		Resources: v1.NodeResources{
			CPU: v1.CPU{
				Quantity: v1.NewResourcePairMilli(0, 0, resource.DecimalSI),
				Info:     cpuInfo,
			},
			GPU: v1.GPU{
				Quantity: v1.NewResourcePair(0, 0, resource.DecimalSI),
				Info:     gpuInfo,
			},
			Memory: v1.Memory{
				Quantity: v1.NewResourcePair(0, 0, resource.DecimalSI),
				Info:     nil,
			},
			EphemeralStorage: v1.NewResourcePair(0, 0, resource.DecimalSI),
			VolumesAttached:  v1.NewResourcePair(0, 0, resource.DecimalSI),
			VolumesMounted:   v1.NewResourcePair(0, 0, resource.DecimalSI),
		},
	}

	switch obj := caps.Capabilities.(type) {
	case *CapabilitiesV1:
		res.Capabilities.StorageClasses = obj.StorageClasses
	default:
	}

	for name, r := range knode.Status.Allocatable {
		switch name {
		case corev1.ResourceCPU:
			res.Resources.CPU.Quantity.Allocatable.SetMilli(r.MilliValue())
		case corev1.ResourceMemory:
			res.Resources.Memory.Quantity.Allocatable.Set(r.Value())
		case corev1.ResourceEphemeralStorage:
			res.Resources.EphemeralStorage.Allocatable.Set(r.Value())
		case builder.ResourceGPUNvidia:
		case builder.ResourceGPUAMD:
			res.Resources.GPU.Quantity.Allocatable.Set(r.Value())
		}
	}

	initPods := make(map[string]corev1.Pod)

	podsList, err := kc.CoreV1().Pods(corev1.NamespaceAll).List(ctx, metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("spec.nodeName", name).String(),
	})
	if err != nil {
		return res, nil, err
	}

	for _, pod := range podsList.Items {
		for _, container := range pod.Spec.Containers {
			addAllocatedResources(&res, container.Resources.Requests)
		}
		initPods[pod.Name] = pod
	}

	return res, initPods, nil
}

func (s *grpcNodeServer) QueryNode(ctx context.Context, _ *emptypb.Empty) (*v1.Node, error) {
	reqch := make(chan v1.Node, 1)

	select {
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	case <-ctx.Done():
		return nil, ctx.Err()
	case s.reqch <- reqch:
	}

	select {
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	case <-ctx.Done():
		return nil, ctx.Err()
	case req := <-reqch:
		return &req, nil
	}
}

func (s *grpcNodeServer) StreamNode(_ *emptypb.Empty, stream v1.NodeRPC_StreamNodeServer) error {
	subch := s.sub.Sub(topicNode)

	defer func() {
		s.sub.Unsub(subch, topicNode)
	}()

	for {
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-stream.Context().Done():
			return stream.Context().Err()
		case nd := <-subch:
			switch msg := nd.(type) {
			case v1.Node:
				if err := stream.Send(&msg); err != nil {
					return err
				}
			default:
			}
		}
	}
}

type hwInfo interface {
	CPU(context.Context) (*cpu.Info, error)
	GPU(context.Context) (*gpu.Info, error)
	Memory(context.Context) (*memory.Info, error)
}

type localHwReader struct{}

func (lfs *localHwReader) CPU(_ context.Context) (*cpu.Info, error) {
	return cpu.New()
}

func (lfs *localHwReader) GPU(_ context.Context) (*gpu.Info, error) {
	return gpu.New()
}

func (lfs *localHwReader) Memory(_ context.Context) (*memory.Info, error) {
	return memory.New()
}

func parseCPUInfo(ctx context.Context) (v1.CPUInfoS, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	hw := HWInfoFromCtx(ctx)

	cpus, err := hw.CPU(ctx)
	if err != nil {
		return nil, err
	}

	res := make(v1.CPUInfoS, 0, len(cpus.Processors))

	for _, c := range cpus.Processors {
		res = append(res, v1.CPUInfo{
			ID:     strconv.Itoa(c.ID),
			Vendor: c.Vendor,
			Model:  c.Model,
			Vcores: c.NumThreads,
		})
	}

	return res, nil
}

func parseGPUInfo(ctx context.Context) (v1.GPUInfoS, error) {
	res := make(v1.GPUInfoS, 0)

	if err := ctx.Err(); err != nil {
		return res, err
	}

	hw := HWInfoFromCtx(ctx)
	gpus, err := hw.GPU(ctx)
	if err != nil {
		return res, err
	}

	if gpus == nil {
		return res, nil
	}

	for _, dev := range gpus.GraphicsCards {
		dinfo := dev.DeviceInfo
		if dinfo == nil {
			continue
		}

		vinfo := dinfo.Vendor
		pinfo := dinfo.Product
		if vinfo == nil || pinfo == nil {
			continue
		}

		vendor, exists := supportedGPUs[vinfo.ID]
		if !exists {
			continue
		}

		model, exists := vendor.Devices[pinfo.ID]
		if !exists {
			continue
		}

		res = append(res, v1.GPUInfo{
			Vendor:     dev.DeviceInfo.Vendor.Name,
			VendorID:   dev.DeviceInfo.Vendor.ID,
			Name:       dev.DeviceInfo.Product.Name,
			ModelID:    dev.DeviceInfo.Product.ID,
			Interface:  model.Interface,
			MemorySize: model.MemorySize,
		})
	}

	return res, nil
}

func (dp *debuggerPod) CPU(ctx context.Context) (*cpu.Info, error) {
	respch := make(chan dpReadResp, 1)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-dp.ctx.Done():
		return nil, dp.ctx.Err()
	case dp.readch <- dpReadReq{
		op:   dpReqCPU,
		resp: respch,
	}:
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-dp.ctx.Done():
		return nil, dp.ctx.Err()
	case resp := <-respch:
		return resp.data.(*cpu.Info), resp.err
	}
}

func (dp *debuggerPod) GPU(ctx context.Context) (*gpu.Info, error) {
	respch := make(chan dpReadResp, 1)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-dp.ctx.Done():
		return nil, dp.ctx.Err()
	case dp.readch <- dpReadReq{
		op:   dpReqGPU,
		resp: respch,
	}:
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-dp.ctx.Done():
		return nil, dp.ctx.Err()
	case resp := <-respch:
		return resp.data.(*gpu.Info), resp.err
	}
}

func (dp *debuggerPod) Memory(ctx context.Context) (*memory.Info, error) {
	respch := make(chan dpReadResp, 1)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-dp.ctx.Done():
		return nil, dp.ctx.Err()
	case dp.readch <- dpReadReq{
		op:   dpReqMem,
		resp: respch,
	}:
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-dp.ctx.Done():
		return nil, dp.ctx.Err()
	case resp := <-respch:
		return resp.data.(*memory.Info), resp.err
	}
}

func (dp *debuggerPod) run(startch chan<- struct{}) error {
	log := fromctx.LogrFromCtx(dp.ctx)

	log.Info("staring debugger pod")

	req := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "fd-debugger-pod",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "psutil",
					Image: "ghcr.io/akash-network/provider-test:latest-arm64",
					Command: []string{
						"provider-services",
						"operator",
						"psutil",
						"serve",
						"--api-port=8081",
					},
					Ports: []corev1.ContainerPort{
						{
							Name:          "api",
							ContainerPort: 8081,
						},
					},
				},
			},
		},
	}

	kc := fromctx.KubeClientFromCtx(dp.ctx)

	pod, err := kc.CoreV1().Pods(daemonSetNamespace).Create(dp.ctx, req, metav1.CreateOptions{})
	if err != nil && !kerrors.IsAlreadyExists(err) {
		return err
	}

	defer func() {
		// using default context here to delete pod as main might have been canceled
		_ = kc.CoreV1().Pods(daemonSetNamespace).Delete(context.Background(), pod.Name, metav1.DeleteOptions{})
	}()

	watcher, err := kc.CoreV1().Pods(daemonSetNamespace).Watch(dp.ctx, metav1.ListOptions{
		Watch:           true,
		ResourceVersion: pod.ResourceVersion,
		FieldSelector:   fields.Set{"metadata.name": pod.Name}.AsSelector().String(),
		LabelSelector:   labels.Everything().String(),
	})

	if err != nil {
		return err
	}

	defer func() {
		watcher.Stop()
	}()

	var apiPort int32

	for _, container := range pod.Spec.Containers {
		if container.Name == "psutil" {
			for _, port := range container.Ports {
				if port.Name == "api" {
					apiPort = port.ContainerPort
				}
			}
		}
	}

	if apiPort == 0 {
		return fmt.Errorf("debugger pod does not have port named \"api\"") // nolint: goerr113
	}

initloop:
	for {
		select {
		case <-dp.ctx.Done():
			return dp.ctx.Err()
		case evt := <-watcher.ResultChan():
			resp := evt.Object.(*corev1.Pod)
			if resp.Status.Phase != corev1.PodPending {
				watcher.Stop()
				startch <- struct{}{}
				break initloop
			}
		}
	}

	for {
		select {
		case <-dp.ctx.Done():
			return dp.ctx.Err()
		case readreq := <-dp.readch:
			var res string
			resp := dpReadResp{}

			switch readreq.op {
			case dpReqCPU:
				res = "cpu"
			case dpReqGPU:
				res = "gpu"
			case dpReqMem:
				res = "memory"
			}

			result := kc.CoreV1().RESTClient().Get().
				Namespace(daemonSetNamespace).
				Resource("pods").
				Name(fmt.Sprintf("%s:%d", pod.Name, apiPort)).
				SubResource("proxy").
				Suffix(res).
				Do(dp.ctx)

			resp.err = result.Error()

			if resp.err == nil {
				var data []byte
				data, resp.err = result.Raw()
				if resp.err == nil {
					switch readreq.op {
					case dpReqCPU:
						var res cpu.Info
						resp.err = json.Unmarshal(data, &res)
						resp.data = &res
					case dpReqGPU:
						var res gpu.Info
						resp.err = json.Unmarshal(data, &res)
						resp.data = &res
					case dpReqMem:
						var res memory.Info
						resp.err = json.Unmarshal(data, &res)
						resp.data = &res
					}
				}
			}

			readreq.resp <- resp
		}
	}
}

// // ExecCmd exec command on specific pod and wait the command's output.
// func ExecCmd(ctx context.Context, podName string, command string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
// 	kc := KubeClientFromCtx(ctx)
// 	cfg := KubeConfigFromCtx(ctx)
//
// 	cmd := []string{
// 		"sh",
// 		"-c",
// 		command,
// 	}
//
// 	option := &corev1.PodExecOptions{
// 		Command: cmd,
// 		Stdin:   true,
// 		Stdout:  true,
// 		Stderr:  true,
// 		TTY:     true,
// 	}
// 	if stdin == nil {
// 		option.Stdin = false
// 	}
//
// 	req := kc.CoreV1().
// 		RESTClient().
// 		Post().
// 		Resource("pods").
// 		Name(podName).
// 		Namespace(daemonSetNamespace).
// 		SubResource("exec").
// 		VersionedParams(option, scheme.ParameterCodec)
//
// 	exec, err := remotecommand.NewSPDYExecutor(cfg, "POST", req.URL())
// 	if err != nil {
// 		return err
// 	}
// 	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
// 		Stdin:  stdin,
// 		Stdout: stdout,
// 		Stderr: stderr,
// 	})
// 	if err != nil {
// 		return err
// 	}
//
// 	return nil
// }
