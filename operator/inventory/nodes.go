package inventory

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/go-logr/logr"
	"github.com/troian/pubsub"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"

	inventoryV1 "github.com/akash-network/akash-api/go/inventory/v1"

	"github.com/akash-network/provider/tools/fromctx"
)

type nodeStateEnum int

const (
	nodeStateUpdated nodeStateEnum = iota
	nodeStateRemoved
)

type nodeState struct {
	state nodeStateEnum
	name  string
	node  inventoryV1.Node
}

type clusterNodes struct {
	querierNodes
	ctx        context.Context
	group      *errgroup.Group
	log        logr.Logger
	kc         *kubernetes.Clientset
	signaldone chan string
	image      string
	namespace  string
}

func newClusterNodes(ctx context.Context, image, namespace string) *clusterNodes {
	log := fromctx.LogrFromCtx(ctx).WithName("nodes")

	group, ctx := errgroup.WithContext(ctx)

	fd := &clusterNodes{
		querierNodes: newQuerierNodes(),
		log:          log,
		ctx:          logr.NewContext(ctx, log),
		group:        group,
		kc:           fromctx.KubeClientFromCtx(ctx),
		signaldone:   make(chan string, 1),
		image:        image,
		namespace:    namespace,
	}

	leftovers, _ := fd.kc.CoreV1().Pods(namespace).List(fd.ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=inventory" +
			",app.kubernetes.io/instance=inventory-hardware-discovery" +
			",app.kubernetes.io/component=operator" +
			",app.kubernetes.io/part-of=provider",
	})

	for _, pod := range leftovers.Items {
		_ = fd.kc.CoreV1().Pods(namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
	}

	group.Go(fd.connector)
	group.Go(fd.run)

	return fd
}

func (cl *clusterNodes) Wait() error {
	return cl.group.Wait()
}

func (cl *clusterNodes) connector() error {
	ctx := cl.ctx
	bus := fromctx.PubSubFromCtx(ctx)
	log := fromctx.LogrFromCtx(ctx).WithName("nodes")

	events := bus.Sub(topicKubeNodes)
	nodes := make(map[string]*nodeDiscovery)

	nctx, ncancel := context.WithCancel(ctx)
	defer func() {
		ncancel()

		for name, node := range nodes {
			_ = node.shutdown()
			delete(nodes, name)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case name := <-cl.signaldone:
			if node, exists := nodes[name]; exists {
				delete(nodes, node.name)

				err := node.shutdown()
				if err != nil && !errors.Is(err, context.Canceled) {
					log.Error(err, fmt.Sprintf("\"%s\" exited with error. attempting restart", name))
				}
			}
			nodes[name] = newNodeDiscovery(nctx, name, cl.namespace, cl.image, cl.signaldone)
		case rEvt := <-events:
			switch evt := rEvt.(type) {
			case watch.Event:
				switch obj := evt.Object.(type) {
				case *corev1.Node:
					switch evt.Type {
					case watch.Added:
						nodes[obj.Name] = newNodeDiscovery(nctx, obj.Name, cl.namespace, cl.image, cl.signaldone)
					case watch.Deleted:
						if node, exists := nodes[obj.Name]; exists {
							_ = node.shutdown()
							delete(nodes, node.name)
						}
					}
				}
			}
		}
	}
}

func (cl *clusterNodes) run() error {
	nodes := make(map[string]inventoryV1.Node)

	snapshot := func() inventoryV1.Nodes {
		res := make(inventoryV1.Nodes, 0, len(nodes))

		for _, nd := range nodes {
			res = append(res, nd.Dup())
		}

		sort.Sort(res)

		return res
	}

	bus := fromctx.PubSubFromCtx(cl.ctx)

	events := bus.Sub(topicInventoryNode)
	defer bus.Unsub(events)
	for {
		select {
		case <-cl.ctx.Done():
			return cl.ctx.Err()
		case revt := <-events:
			switch evt := revt.(type) {
			case nodeState:
				switch evt.state {
				case nodeStateUpdated:
					nodes[evt.name] = evt.node
				case nodeStateRemoved:
					delete(nodes, evt.name)
				}

				bus.Pub(snapshot(), []string{topicInventoryNodes}, pubsub.WithRetain())
			default:
			}
		case req := <-cl.reqch:
			resp := respNodes{
				res: snapshot(),
			}

			req.respCh <- resp
		}
	}
}
