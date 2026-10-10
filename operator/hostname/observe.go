package hostname

import (
	"context"
	"fmt"

	sdktypes "github.com/cosmos/cosmos-sdk/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/pager"

	mtypes "pkg.akt.dev/go/node/market/v1"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

func hostnameEventFromProviderHost(ph *crd.ProviderHost, eventType ctypes.ProviderResourceEvent) (hostnameResourceEvent, error) {
	owner, err := sdktypes.AccAddressFromBech32(ph.Spec.Owner)
	if err != nil {
		return hostnameResourceEvent{}, fmt.Errorf("invalid owner address in provider host %q: %w", ph.Name, err)
	}
	provider, err := sdktypes.AccAddressFromBech32(ph.Spec.Provider)
	if err != nil {
		return hostnameResourceEvent{}, fmt.Errorf("invalid provider address in provider host %q: %w", ph.Name, err)
	}
	return hostnameResourceEvent{
		eventType: eventType,
		hostname:  ph.Spec.Hostname,
		leaseID: mtypes.LeaseID{
			Owner: owner.String(), Provider: provider.String(),
			DSeq: ph.Spec.Dseq, GSeq: ph.Spec.Gseq, OSeq: ph.Spec.Oseq,
		},
		serviceName:  ph.Spec.ServiceName,
		externalPort: ph.Spec.ExternalPort,
	}, nil
}

func (op *hostnameOperator) listHostnameState(ctx context.Context) ([]hostnameResourceEvent, string, error) {
	var resourceVersion string
	phpager := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
		resources, err := op.ac.AkashV2beta2().ProviderHosts(op.ns).List(ctx, opts)
		if err == nil {
			resourceVersion = resources.ResourceVersion
		}
		return resources, err
	})

	snapshot := make([]hostnameResourceEvent, 0, 128)
	err := phpager.EachListItem(ctx, metav1.ListOptions{}, func(obj runtime.Object) error {
		ph, ok := obj.(*crd.ProviderHost)
		if !ok || ph == nil {
			op.log.Error("unexpected object in provider host list", "type", fmt.Sprintf("%T", obj))
			return nil
		}
		ev, err := hostnameEventFromProviderHost(ph, ctypes.ProviderResourceAdd)
		if err != nil {
			op.log.Error("invalid provider host", "name", ph.Name, "err", err)
			return nil
		}
		snapshot = append(snapshot, ev)
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return snapshot, resourceVersion, nil
}

func (op *hostnameOperator) observeHostnameState(ctx context.Context) (<-chan chostname.ResourceEvent, error) {
	snapshot, resourceVersion, err := op.listHostnameState(ctx)
	if err != nil {
		return nil, err
	}

	// Continue from the complete list snapshot so updates and deletions between
	// List and Watch are delivered instead of leaving stale routes behind.
	op.log.Info("starting hostname watch", "resourceVersion", resourceVersion)
	watcher, err := op.ac.AkashV2beta2().ProviderHosts(op.ns).Watch(ctx, metav1.ListOptions{
		ResourceVersion: resourceVersion,
	})
	if err != nil {
		return nil, err
	}

	output := make(chan chostname.ResourceEvent)
	go func() {
		defer close(output)
		defer watcher.Stop()

		for _, ev := range snapshot {
			select {
			case output <- ev:
			case <-ctx.Done():
				return
			}
		}
		snapshot = nil

		results := watcher.ResultChan()
		for {
			select {
			case <-ctx.Done():
				return
			case result, ok := <-results:
				if !ok {
					return
				}
				var eventType ctypes.ProviderResourceEvent
				switch result.Type {
				case watch.Added:
					eventType = ctypes.ProviderResourceAdd
				case watch.Modified:
					eventType = ctypes.ProviderResourceUpdate
				case watch.Deleted:
					eventType = ctypes.ProviderResourceDelete
				case watch.Error:
					op.log.Error("hostname watch error", "err", result.Object)
					return
				default:
					// Bookmarks do not describe a ProviderHost change.
					continue
				}

				ph, ok := result.Object.(*crd.ProviderHost)
				if !ok || ph == nil {
					op.log.Error("unexpected object in hostname watch", "type", fmt.Sprintf("%T", result.Object))
					continue
				}
				ev, err := hostnameEventFromProviderHost(ph, eventType)
				if err != nil {
					op.log.Error("invalid provider host", "name", ph.Name, "err", err)
					continue
				}
				select {
				case output <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return output, nil
}
