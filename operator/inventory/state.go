package inventory

import (
	"context"

	"github.com/troian/pubsub"

	inventory "pkg.akt.dev/go/inventory/v1"

	"github.com/akash-network/provider/tools/fromctx"
)

type clusterState struct {
	ctx context.Context
	querierCluster
}

func (s *clusterState) run() error {
	bus, err := fromctx.PubSubFromCtx(s.ctx)
	if err != nil {
		return err
	}

	storage := make(map[string]inventory.ClusterStorage)

	state := inventory.Cluster{}
	signalch := make(chan struct{}, 1)

	datach := bus.Sub(topicInventoryNodes, topicInventoryStorage, topicInventoryConfig)

	defer bus.Unsub(datach)

	var cfg Config

	trySignal := func() {
		select {
		case signalch <- struct{}{}:
		default:
		}
	}

	for {
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case data := <-datach:
			switch obj := data.(type) {
			case Config:
				cfg = obj
			case inventory.Nodes:
				state.Nodes = obj
				trySignal()
			case storageSignal:
				storage[obj.driver] = obj.storage

				prealloc := 0
				for _, drv := range storage {
					prealloc += len(drv)
				}

				state.Storage = make(inventory.ClusterStorage, 0, prealloc)

				for _, drv := range storage {
					for _, class := range drv {
						if !cfg.HasStorageClass(class.Info.Class) {
							continue
						}

						state.Storage = append(state.Storage, class)
					}
				}

				trySignal()
			default:
			}
		case req := <-s.reqch:
			req.respCh <- respCluster{
				res: *state.Dup(),
				err: nil,
			}
		case <-signalch:
			bus.Pub(*state.Dup(), []string{topicInventoryCluster}, pubsub.WithRetain())
		}
	}
}
