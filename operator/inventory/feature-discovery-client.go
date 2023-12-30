package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	inventoryV1 "github.com/akash-network/akash-api/go/inventory/v1"
	"github.com/go-logr/logr"
	"github.com/troian/pubsub"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/tools/fromctx"
)

type nodeStateEnum int

const (
	daemonSetNamespace = "akash-services"
	reconnectTimeout   = 5 * time.Second
)

const (
	nodeStateUpdated nodeStateEnum = iota
	nodeStateRemoved
)

type k8sPatch struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value"`
}

type podStream struct {
	ctx      context.Context
	cancel   context.CancelFunc
	group    *errgroup.Group
	log      logr.Logger
	nodeName string
	address  string
	port     uint16
}

type nodeState struct {
	state nodeStateEnum
	name  string
	node  inventoryV1.Node
}

type featureDiscovery struct {
	querierNodes
	ctx   context.Context
	group *errgroup.Group
	log   logr.Logger
	kc    *kubernetes.Clientset
}

func newFeatureDiscovery(ctx context.Context) *featureDiscovery {
	log := fromctx.LogrFromCtx(ctx).WithName("feature-discovery")

	group, ctx := errgroup.WithContext(ctx)

	fd := &featureDiscovery{
		log:          log,
		ctx:          logr.NewContext(ctx, log),
		group:        group,
		kc:           fromctx.KubeClientFromCtx(ctx),
		querierNodes: newQuerierNodes(),
	}

	group.Go(fd.connectorRun)
	group.Go(fd.run)
	group.Go(fd.nodeLabeler)

	return fd
}

func (fd *featureDiscovery) Wait() error {
	return fd.group.Wait()
}

func (fd *featureDiscovery) connectorRun() error {
	watcher, err := fd.kc.CoreV1().Pods(daemonSetNamespace).Watch(fd.ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=inventory" +
			",app.kubernetes.io/instance=inventory-node" +
			",app.kubernetes.io/component=operator" +
			",app.kubernetes.io/part-of=provider",
	})

	if err != nil {
		return fmt.Errorf("error setting up Kubernetes watcher: %w", err)
	}

	nodes := make(map[string]*podStream)

	for {
		select {
		case <-fd.ctx.Done():
			for _, nd := range nodes {
				nd.cancel()
				delete(nodes, nd.nodeName)
			}

			return fd.ctx.Err()
		case event := <-watcher.ResultChan():
			if obj, valid := event.Object.(*corev1.Pod); valid {
				nodeName := obj.Spec.NodeName

				switch event.Type {
				case watch.Added:
					fallthrough
				case watch.Modified:
					if obj.Status.Phase == corev1.PodRunning && obj.Status.PodIP != "" {
						if _, exists := nodes[nodeName]; exists {
							continue
						}

						var containerPort uint16

						for _, container := range obj.Spec.Containers {
							if container.Name == fdContainerName {
								for _, port := range container.Ports {
									if port.Name == fdContainerGRPCPortName {
										containerPort = uint16(port.ContainerPort)
										break
									}
								}
								break
							}
						}

						nodes[nodeName], err = newNodeWatcher(fd.ctx, nodeName, obj.Name, obj.Status.PodIP, containerPort)
						if err != nil {
							return err
						}
					}
				case watch.Deleted:
					nd, exists := nodes[nodeName]
					if !exists {
						continue
					}

					nd.cancel()
					delete(nodes, nodeName)
				}
			}
		}
	}
}

func (fd *featureDiscovery) run() error {
	nodes := make(map[string]inventoryV1.Node)

	snapshot := func() inventoryV1.Nodes {
		res := make(inventoryV1.Nodes, 0, len(nodes))

		for _, nd := range nodes {
			res = append(res, nd.Dup())
		}

		return res
	}

	bus := fromctx.PubSubFromCtx(fd.ctx)

	events := bus.Sub(topicNodeState)
	defer bus.Unsub(events)
	for {
		select {
		case <-fd.ctx.Done():
			return fd.ctx.Err()
		case revt := <-events:
			switch evt := revt.(type) {
			case nodeState:
				switch evt.state {
				case nodeStateUpdated:
					nodes[evt.name] = evt.node
				case nodeStateRemoved:
					delete(nodes, evt.name)
				}

				bus.Pub(snapshot(), []string{topicNodes}, pubsub.WithRetain())
			default:
			}
		case req := <-fd.reqch:
			resp := respNodes{
				res: snapshot(),
			}

			req.respCh <- resp
		}
	}
}

func (fd *featureDiscovery) nodeLabeler() error {
	bus := fromctx.PubSubFromCtx(fd.ctx)
	log := fromctx.LogrFromCtx(fd.ctx)

	var unsubChs []<-chan interface{}
	var eventsConfigBackup <-chan interface{}
	var eventsBackup <-chan interface{}
	var events <-chan interface{}

	eventsConfig := bus.Sub("config")
	unsubChs = append(unsubChs, eventsConfig)

	configReloadCh := make(chan struct{}, 1)

	defer func() {
		for _, ch := range unsubChs {
			bus.Unsub(ch)
		}
	}()

	var cfg Config

	signalConfigReload := func() {
		select {
		case configReloadCh <- struct{}{}:
			eventsConfigBackup = eventsConfig
			eventsBackup = events

			events = nil
			eventsConfig = nil
		default:
		}
	}

	for {
		select {
		case <-fd.ctx.Done():
			return fd.ctx.Err()
		case <-configReloadCh:
			log.Info("received signal to rebuild config. invalidating all inventory and restarting query process")
			fd.reloadConfig(cfg)

			events = eventsBackup
			eventsConfig = eventsConfigBackup

			if events == nil {
				events = bus.Sub("nodes", "sc")
				unsubChs = append(unsubChs, events)
			}

		case rawEvt := <-events:
			if evt, valid := rawEvt.(watch.Event); valid {
				signal := false

				switch evt.Object.(type) {
				case *corev1.Node:
					signal = (evt.Type == watch.Added) || (evt.Type == watch.Modified)
				case *storagev1.StorageClass:
					signal = evt.Type == watch.Deleted
				}

				if signal {
					signalConfigReload()
				}
			}
		case evt := <-eventsConfig:
			log.Info("received config update")

			cfg = evt.(Config)
			signalConfigReload()
		}
	}
}

func isNodeAkashLabeled(labels map[string]string) bool {
	_, exists := labels[builder.AkashManagedLabelName]

	return exists
}

func isNodeReady(conditions []corev1.NodeCondition) bool {
	for _, c := range conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == "True"
		}
	}

	return false
}

func (fd *featureDiscovery) reloadConfig(cfg Config) {
	log := fromctx.LogrFromCtx(fd.ctx)

	adjConfig := cfg.Copy()

	nodesList, _ := fd.kc.CoreV1().Nodes().List(fd.ctx, metav1.ListOptions{})

	scList, _ := fd.kc.StorageV1().StorageClasses().List(fd.ctx, metav1.ListOptions{})

	presentSc := make([]string, 0, len(scList.Items))
	for _, sc := range scList.Items {
		presentSc = append(presentSc, sc.Name)
	}

	sort.Strings(presentSc)

	adjConfig.FilterOutStorageClasses(presentSc)
	patches := make(map[string][]k8sPatch)

	for _, node := range nodesList.Items {
		var p []k8sPatch

		isExcluded := !isNodeReady(node.Status.Conditions) || node.Spec.Unschedulable || adjConfig.Exclude.IsNodeExcluded(node.Name)

		// node is currently labeled for akash inventory but is excluded from config
		if isNodeAkashLabeled(node.Labels) && isExcluded {
			delete(node.Labels, builder.AkashManagedLabelName)
			delete(node.Annotations, AnnotationKeyCapabilities)

			p = append(p, k8sPatch{
				Op:    "add",
				Path:  "/metadata/labels",
				Value: node.Labels,
			})
			p = append(p, k8sPatch{
				Op:    "add",
				Path:  "/metadata/annotations",
				Value: node.Annotations,
			})
			log.Info(fmt.Sprintf("node \"%s\" has matching exclude rule. removing from intentory", node.Name))
		} else if !isNodeAkashLabeled(node.Labels) && !isExcluded {
			node.Labels[builder.AkashManagedLabelName] = "true"
			p = append(p, k8sPatch{
				Op:    "add",
				Path:  "/metadata/labels",
				Value: node.Labels,
			})
			log.Info(fmt.Sprintf("node \"%s\" is being added to intentory", node.Name))
		}

		if !isExcluded {
			var op string
			caps, _ := parseNodeCapabilities(node.Annotations)
			if caps.Capabilities == nil {
				op = "add"
				caps = NewAnnotationCapabilities(adjConfig.ClusterStorage)
			} else {
				sc := adjConfig.StorageClassesForNode(node.Name)
				switch obj := caps.Capabilities.(type) {
				case *CapabilitiesV1:
					if !slices.Equal(sc, obj.StorageClasses) {
						op = "add"
						obj.StorageClasses = sc
					}
				default:
				}
			}

			if op != "" {
				data, _ := json.Marshal(caps)
				node.Annotations[AnnotationKeyCapabilities] = string(data)

				p = append(p, k8sPatch{
					Op:    "add",
					Path:  "/metadata/annotations",
					Value: node.Annotations,
				})
			}
		}

		if len(p) > 0 {
			patches[node.Name] = p
		}
	}

	for node, p := range patches {
		data, _ := json.Marshal(p)

		_, err := fd.kc.CoreV1().Nodes().Patch(fd.ctx, node, k8stypes.JSONPatchType, data, metav1.PatchOptions{})
		if err != nil {
			log.Error(err, fmt.Sprintf("couldn't apply patches for node \"%s\"", node))
		} else {
			log.Info(fmt.Sprintf("successfully applied labels and/or annotations patches for node \"%s\"", node))
			log.Info(fmt.Sprintf("node %s: %s", node, string(data)))
		}
	}
}

func newNodeWatcher(ctx context.Context, nodeName string, podName string, address string, port uint16) (*podStream, error) {
	ctx, cancel := context.WithCancel(ctx)
	group, ctx := errgroup.WithContext(ctx)

	ps := &podStream{
		ctx:      ctx,
		cancel:   cancel,
		group:    group,
		log:      fromctx.LogrFromCtx(ctx).WithName("node-watcher"),
		nodeName: nodeName,
		address:  address,
		port:     port,
	}

	kubecfg := fromctx.KubeConfigFromCtx(ctx)

	if kubecfg.BearerTokenFile != "/var/run/secrets/kubernetes.io/serviceaccount/token" {
		roundTripper, upgrader, err := spdy.RoundTripperFor(kubecfg)
		if err != nil {
			return nil, err
		}

		path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", daemonSetNamespace, podName)
		hostIP := strings.TrimPrefix(kubecfg.Host, "https://")
		serverURL := url.URL{Scheme: "https", Path: path, Host: hostIP}

		dialer := spdy.NewDialer(upgrader, &http.Client{Transport: roundTripper}, http.MethodPost, &serverURL)

		errch := make(chan error, 1)
		pf, err := portforward.New(dialer, []string{fmt.Sprintf(":%d", port)}, ctx.Done(), make(chan struct{}), os.Stdout, os.Stderr)
		if err != nil {
			return nil, err
		}

		group.Go(func() error {
			err := pf.ForwardPorts()
			errch <- err
			return err
		})

		select {
		case <-pf.Ready:
		case err := <-errch:
			return nil, err
		}

		ports, err := pf.GetPorts()
		if err != nil {
			return nil, err
		}

		ps.address = "localhost"
		ps.port = ports[0].Local
	}

	go ps.run()

	return ps, nil
}

func (nd *podStream) run() {
	// Establish the gRPC connection
	conn, err := grpc.Dial(fmt.Sprintf("%s:%d", nd.address, nd.port), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		nd.log.Error(err, "couldn't dial endpoint")
		return
	}

	defer func() {
		_ = conn.Close()
	}()

	nd.log.Info(fmt.Sprintf("(connected to node's \"%s\" inventory streamer at %s:%d", nd.nodeName, nd.address, nd.port))

	client := inventoryV1.NewNodeRPCClient(conn)

	var stream inventoryV1.NodeRPC_StreamNodeClient

	pub := fromctx.PubSubFromCtx(nd.ctx)

	for {
		for stream == nil {
			conn.Connect()

			if state := conn.GetState(); state != connectivity.Ready {
				if !conn.WaitForStateChange(nd.ctx, connectivity.Ready) {
					return
				}
			}

			// do not replace empty argument with nil. stream will panic
			stream, err = client.StreamNode(nd.ctx, &emptypb.Empty{})
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}

				nd.log.Error(err, "couldn't establish stream")

				tctx, tcancel := context.WithTimeout(nd.ctx, 2*time.Second)
				<-tctx.Done()
				tcancel()

				if !errors.Is(tctx.Err(), context.DeadlineExceeded) {
					return
				}
			}
		}

		node, err := stream.Recv()
		if err != nil {
			pub.Pub(nodeState{
				state: nodeStateRemoved,
				name:  nd.nodeName,
			}, []string{topicNodeState})

			stream = nil

			if errors.Is(err, context.Canceled) {
				return
			}

			conn.ResetConnectBackoff()
		} else {
			pub.Pub(nodeState{
				state: nodeStateUpdated,
				name:  nd.nodeName,
				node:  node.Dup(),
			}, []string{topicNodeState})
		}
	}
}
