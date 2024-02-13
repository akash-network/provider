package inventory

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"golang.org/x/net/context"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	"github.com/tendermint/tendermint/libs/log"

	inventoryV1 "github.com/akash-network/akash-api/go/inventory/v1"

	kutil "github.com/akash-network/provider/cluster/kube/util"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	cinventory "github.com/akash-network/provider/cluster/types/v1beta3/clients/inventory"
	"github.com/akash-network/provider/tools/fromctx"
)

type inventoryEvent int

const (
	inventoryEventUpdated inventoryEvent = iota
	inventoryEventDeleted
)

const (
	runtimeClassNvidia = "nvidia"
)

type client struct {
	ctx   context.Context
	group *errgroup.Group
	subch chan chan<- ctypes.Inventory
}

type inventory struct {
	inventoryV1.Cluster
	log log.Logger
}

type inventoryState struct {
	evt     inventoryEvent
	cluster *inventoryV1.Cluster
}

var (
	_ cinventory.Client = (*client)(nil)
)

func NewClient(ctx context.Context) (cinventory.Client, error) {
	group, ctx := errgroup.WithContext(ctx)

	cl := &client{
		ctx:   ctx,
		subch: make(chan chan<- ctypes.Inventory, 1),
		group: group,
	}

	group.Go(cl.discovery)

	return cl, nil
}

func (cl *client) ResultChan() <-chan ctypes.Inventory {
	ch := make(chan ctypes.Inventory, 1)

	select {
	case <-cl.ctx.Done():
		close(ch)
	case cl.subch <- ch:
	}

	return ch
}

func (cl *client) discovery() error {
	ictx, icancel := context.WithCancel(cl.ctx)
	defer icancel()

	kc := fromctx.MustKubeClientFromCtx(cl.ctx)

	watcher, err := kc.CoreV1().Services("akash-services").Watch(cl.ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=inventory" +
			",app.kubernetes.io/instance=inventory-service" +
			",app.kubernetes.io/component=operator" +
			",app.kubernetes.io/part-of=provider",
	})
	if err != nil {
		return err
	}

	defer watcher.Stop()

	svcEvents := watcher.ResultChan()
	inv := inventoryV1.Cluster{}

	invch := make(chan inventoryState, 1)

	var ctrlexitch <-chan struct{}
	var subs []chan<- inventoryV1.Cluster

	isConnected := false

	var svc *corev1.Service

	const reconnect = 5 * time.Second

	reconnecttm := time.NewTimer(reconnect)

	startConnector := func() {
		if svc == nil {
			return
		}

		icancel()

		select {
		case <-ctrlexitch:
		default:
		}

		ctrlexitch = nil

		select {
		case <-invch:
		default:
		}

		isConnected = false
		ictx, icancel = context.WithCancel(cl.ctx)
		ctrlexitch, err = newInventoryConnector(ictx, svc, invch)
		if err != nil {
			reconnecttm.Reset(reconnect)
		}
	}

	for {
		select {
		case <-cl.ctx.Done():
			return cl.ctx.Err()
		case <-ctrlexitch:
			startConnector()
		case <-reconnecttm.C:
			startConnector()
		case evt := <-svcEvents:
			switch obj := evt.Object.(type) {
			case *corev1.Service:
				switch evt.Type {
				case watch.Added:
					svc = obj
					startConnector()
				case watch.Deleted:
					svc = nil
					icancel()
					if !reconnecttm.Stop() {
						<-reconnecttm.C
					}
					inv = inventoryV1.Cluster{}
				}
			}
		case state := <-invch:
			if !isConnected {
				isConnected = true
			}

			switch state.evt {
			case inventoryEventUpdated:
				inv = *state.cluster
			case inventoryEventDeleted:
				inv = inventoryV1.Cluster{}
			}

			for _, ch := range subs {
				ch <- *inv.Dup()
			}
		case reqch := <-cl.subch:
			ch := make(chan inventoryV1.Cluster, 1)

			subs = append(subs, ch)

			cl.group.Go(func() error {
				return cl.subscriber(ch, reqch)
			})

			if isConnected {
				ch <- *inv.Dup()
			}
		}
	}
}

func (cl *client) subscriber(in <-chan inventoryV1.Cluster, out chan<- ctypes.Inventory) error {
	defer close(out)

	var pending []inventoryV1.Cluster
	var msg ctypes.Inventory
	var och chan<- ctypes.Inventory

	ilog := fromctx.LogcFromCtx(cl.ctx).With("inventory.adjuster")

	for {
		select {
		case <-cl.ctx.Done():
			return cl.ctx.Err()
		case inv := <-in:
			pending = append(pending, inv)
			if och == nil {
				msg = newInventory(ilog, pending[0])
				och = out
			}
		case och <- msg:
			pending = pending[1:]
			if len(pending) > 0 {
				msg = newInventory(ilog, pending[0])
			} else {
				och = nil
				msg = nil
			}
		}
	}
}

func newInventoryConnector(ctx context.Context, svc *corev1.Service, invch chan<- inventoryState) (<-chan struct{}, error) {
	group, ctx := errgroup.WithContext(ctx)

	var svcPort int32

	for _, sport := range svc.Spec.Ports {
		if sport.Name == "grpc" {
			svcPort = sport.Port
			break
		}
	}

	endpoint := fmt.Sprintf("%s.%s.svc.cluster.local:%d", svc.Name, svc.Namespace, svcPort)

	sigdone := make(chan struct{}, 1)

	isUnderTest := fromctx.IsInventoryUnderTestFromCtx(ctx)

	if !kutil.IsInsideKubernetes() || isUnderTest {
		kc := fromctx.MustKubeClientFromCtx(ctx)

		pods, err := kc.CoreV1().Pods(svc.Namespace).List(ctx, metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/name=inventory" +
				",app.kubernetes.io/instance=inventory-service" +
				",app.kubernetes.io/component=operator" +
				",app.kubernetes.io/part-of=provider",
		})
		if err != nil {
			return nil, err
		}

		if len(pods.Items) == 0 {
			return nil, fmt.Errorf("no inventory pods available") // nolint: goerr113
		}

		var pod *corev1.Pod

		for i, p := range pods.Items {
			if p.Status.Phase == corev1.PodRunning {
				pod = &pods.Items[i]
				break
			}
		}

		if pod == nil {
			return nil, fmt.Errorf("no inventory pods available") // nolint: goerr113
		}

	loop:
		for _, container := range pod.Spec.Containers {
			if container.Name == "operator-inventory" {
				for _, port := range container.Ports {
					if port.Name == "grpc" {
						svcPort = port.ContainerPort
						break loop
					}
				}
			}
		}

		var ports []portforward.ForwardedPort
		if !isUnderTest {
			kubecfg := fromctx.MustKubeConfigFromCtx(ctx)
			roundTripper, upgrader, err := spdy.RoundTripperFor(kubecfg)
			if err != nil {
				return nil, err
			}

			hcl := &http.Client{Transport: roundTripper}

			req := kc.Discovery().RESTClient().Post().
				Prefix("/api/v1").
				Resource("pods").
				Namespace(pod.Namespace).
				Name(pod.Name).
				SubResource("portforward")

			dialer := spdy.NewDialer(upgrader, hcl, http.MethodPost, req.URL())

			errch := make(chan error, 1)
			pf, err := portforward.New(dialer, []string{fmt.Sprintf(":%d", svcPort)}, ctx.Done(), make(chan struct{}), os.Stdout, os.Stderr)
			if err != nil {
				return nil, err
			}

			group.Go(func() error {
				err := pf.ForwardPorts()
				errch <- err
				return err
			})

			group.Go(func() error {
				<-ctx.Done()
				pf.Close()

				return ctx.Err()
			})

			select {
			case <-pf.Ready:
			case err := <-errch:
				return nil, err
			}

			if ports, err = pf.GetPorts(); err != nil {
				return nil, err
			}
		} else {
			ports = append(ports, portforward.ForwardedPort{Local: uint16(svcPort)})
		}

		endpoint = fmt.Sprintf("localhost:%d", ports[0].Local)
	}

	group.Go(func() error {
		return inventoryRun(ctx, endpoint, invch)
	})

	group.Go(func() error {
		<-ctx.Done()

		sigdone <- struct{}{}

		return ctx.Err()
	})

	return sigdone, nil
}

func inventoryRun(ctx context.Context, endpoint string, invch chan<- inventoryState) error {
	log := fromctx.LogcFromCtx(ctx).With("inventory")

	// Establish the gRPC connection
	conn, err := grpc.Dial(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		return err
	}

	defer func() {
		_ = conn.Close()
	}()

	log.Info(fmt.Sprintf("dialing inventory operator at %s", endpoint))

	client := inventoryV1.NewClusterRPCClient(conn)

	var stream inventoryV1.ClusterRPC_StreamClusterClient

	for {
		for stream == nil {
			if state := conn.GetState(); state != connectivity.Ready {
				if !conn.WaitForStateChange(ctx, connectivity.Ready) {
					return ctx.Err()
				}
			}

			// do not replace empty argument with nil. stream will panic
			stream, err = client.StreamCluster(ctx, &emptypb.Empty{})
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return err
				}

				tctx, tcancel := context.WithTimeout(ctx, 2*time.Second)
				<-tctx.Done()
				tcancel()

				if !errors.Is(tctx.Err(), context.DeadlineExceeded) {
					return err
				}
			}
		}

		cluster, err := stream.Recv()
		if err == nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case invch <- inventoryState{
				evt:     inventoryEventUpdated,
				cluster: cluster,
			}:
			}

			continue
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case invch <- inventoryState{
			evt: inventoryEventDeleted,
		}:
		}

		stream = nil

		if errors.Is(err, context.Canceled) {
			return err
		}

		conn.ResetConnectBackoff()
	}
}
