package kube

import (
	"context"
	"fmt"
	"strings"
	"time"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/pager"

	sdk "github.com/cosmos/cosmos-sdk/types"
	mtypes "pkg.akt.dev/go/node/market/v1"

	"github.com/akash-network/provider/cluster/kube/builder"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

type hostnameResourceEvent struct {
	eventType    ctypes.ProviderResourceEvent
	hostname     string
	lid          mtypes.LeaseID
	serviceName  string
	externalPort uint32
}

func (c *client) DeclareHostname(ctx context.Context, lID mtypes.LeaseID, host string, serviceName string, externalPort uint32) error {
	// Label each entry with the standard labels
	labels := map[string]string{
		builder.AkashManagedLabelName: "true",
	}

	builder.AppendLeaseLabels(lID, labels)

	update := true
	obj, err := c.ac.AkashV2beta2().ProviderHosts(c.ns).Get(ctx, host, metav1.GetOptions{})
	if err != nil {
		if kerrors.IsNotFound(err) {
			update = false
		} else {
			return err
		}
	}

	if !update {
		obj = &crd.ProviderHost{
			ObjectMeta: metav1.ObjectMeta{
				Name:   host, // Name is always the hostname, to prevent duplicates
				Labels: labels,
			},
			Spec: crd.ProviderHostSpec{
				Hostname:     host,
				Owner:        lID.GetOwner(),
				Dseq:         lID.GetDSeq(),
				Oseq:         lID.GetOSeq(),
				Gseq:         lID.GetGSeq(),
				Provider:     lID.GetProvider(),
				ServiceName:  serviceName,
				ExternalPort: externalPort,
			},
		}
	} else {
		obj.Labels = labels
		obj.Spec = crd.ProviderHostSpec{
			Hostname:     host,
			Owner:        lID.GetOwner(),
			Dseq:         lID.GetDSeq(),
			Oseq:         lID.GetOSeq(),
			Gseq:         lID.GetGSeq(),
			Provider:     lID.GetProvider(),
			ServiceName:  serviceName,
			ExternalPort: externalPort,
		}
	}
	c.log.Info("declaring hostname", "lid", lID, "service", serviceName, "external_port", externalPort, "hostname", host)

	if obj.Annotations == nil {
		obj.Annotations = make(map[string]string)
	}

	obj.Annotations[builder.AkashLeaseUpdatedAt] = time.Now().UTC().Format(time.RFC3339)

	if update {
		_, err = c.ac.AkashV2beta2().ProviderHosts(c.ns).Update(ctx, obj, metav1.UpdateOptions{})
	} else {
		_, err = c.ac.AkashV2beta2().ProviderHosts(c.ns).Create(ctx, obj, metav1.CreateOptions{})
	}

	if err != nil {
		return err
	}

	return nil
}

func (c *client) PurgeDeclaredHostname(ctx context.Context, lID mtypes.LeaseID, hostname string) error {
	labelSelector := &strings.Builder{}
	kubeSelectorForLease(labelSelector, lID)

	return c.ac.AkashV2beta2().ProviderHosts(c.ns).DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{
		LabelSelector: labelSelector.String(),
		FieldSelector: fmt.Sprintf("metadata.name=%s", hostname),
	})
}

func (c *client) PurgeDeclaredHostnames(ctx context.Context, lID mtypes.LeaseID) error {
	labelSelector := &strings.Builder{}
	kubeSelectorForLease(labelSelector, lID)
	result := c.ac.AkashV2beta2().ProviderHosts(c.ns).DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{
		LabelSelector: labelSelector.String(),
	})

	return result
}

func (ev hostnameResourceEvent) GetLeaseID() mtypes.LeaseID {
	return ev.lid
}

func (ev hostnameResourceEvent) GetHostname() string {
	return ev.hostname
}

func (ev hostnameResourceEvent) GetEventType() ctypes.ProviderResourceEvent {
	return ev.eventType
}

func (ev hostnameResourceEvent) GetServiceName() string {
	return ev.serviceName
}

func (ev hostnameResourceEvent) GetExternalPort() uint32 {
	return ev.externalPort
}

func (c *client) ObserveHostnameState(ctx context.Context) (<-chan chostname.ResourceEvent, error) {
	var lastResourceVersion string
	phpager := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
		resources, err := c.ac.AkashV2beta2().ProviderHosts(c.ns).List(ctx, opts)

		if err == nil && len(resources.GetResourceVersion()) != 0 {
			lastResourceVersion = resources.GetResourceVersion()
		}
		return resources, err
	})

	data := make([]crd.ProviderHost, 0, 128)
	err := phpager.EachListItem(ctx, metav1.ListOptions{}, func(obj runtime.Object) error {
		ph := obj.(*crd.ProviderHost)
		data = append(data, *ph)
		return nil
	})

	if err != nil {
		return nil, err
	}

	c.log.Info("starting hostname watch", "resource_version", lastResourceVersion)
	watcher, err := c.ac.AkashV2beta2().ProviderHosts(c.ns).Watch(ctx, metav1.ListOptions{
		TypeMeta:             metav1.TypeMeta{},
		LabelSelector:        "",
		FieldSelector:        "",
		Watch:                false,
		AllowWatchBookmarks:  false,
		ResourceVersion:      lastResourceVersion,
		ResourceVersionMatch: "",
		TimeoutSeconds:       nil,
		Limit:                0,
		Continue:             "",
	})
	if err != nil {
		return nil, err
	}

	// a resource whose lease ID cannot be built is skipped rather than emitted without a type
	evData := make([]hostnameResourceEvent, 0, len(data))
	for _, v := range data {
		lid, err := v.Spec.LeaseID()
		if err != nil {
			c.log.Error("failed to convert LeaseID from CRD resource", "hostname", v.Spec.Hostname, "err", err)
			continue
		}

		evData = append(evData, hostnameResourceEvent{
			eventType:    ctypes.ProviderResourceAdd,
			lid:          lid,
			hostname:     v.Spec.Hostname,
			serviceName:  v.Spec.ServiceName,
			externalPort: v.Spec.ExternalPort,
		})
	}

	data = nil

	output := make(chan chostname.ResourceEvent)

	// every send also waits on ctx, so a consumer that stops reading and cancels ctx
	// releases the producer
	go func() {
		defer func() {
			close(output)
			watcher.Stop()
		}()

		for _, v := range evData {
			select {
			case output <- v:
			case <-ctx.Done():
				return
			}
		}
		evData = nil // do not hold the reference

		results := watcher.ResultChan()
		for {
			select {
			case result, ok := <-results:
				if !ok { // Channel closed when an error happens
					return
				}

				// an error event carries a *metav1.Status; end the stream so the
				// consumer restarts the observation
				if result.Type == watch.Error {
					c.log.Error("watch error", "err", kerrors.FromObject(result.Object))
					return
				}

				ph, ok := result.Object.(*crd.ProviderHost)
				if !ok {
					c.log.Error("unexpected object in hostname watch", "err", fmt.Errorf("unexpected object type %T", result.Object))
					continue
				}

				lid, err := ph.Spec.LeaseID()
				if err != nil {
					c.log.Error("failed to convert LeaseID from CRD resource", "hostname", ph.Spec.Hostname, "err", err)
					continue
				}

				ev := hostnameResourceEvent{
					hostname:     ph.Spec.Hostname,
					lid:          lid,
					serviceName:  ph.Spec.ServiceName,
					externalPort: ph.Spec.ExternalPort,
				}
				switch result.Type {

				case watch.Added:
					ev.eventType = ctypes.ProviderResourceAdd
				case watch.Modified:
					ev.eventType = ctypes.ProviderResourceUpdate
				case watch.Deleted:
					ev.eventType = ctypes.ProviderResourceDelete

				default:

					continue
				}

				select {
				case output <- ev:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return output, nil
}

func (c *client) AllHostnames(ctx context.Context) ([]chostname.ActiveHostname, error) {
	ingressPager := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
		return c.ac.AkashV2beta2().ProviderHosts(c.ns).List(ctx, opts)
	})

	listOptions := metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=true", builder.AkashManagedLabelName),
	}

	result := make([]chostname.ActiveHostname, 0)

	err := ingressPager.EachListItem(ctx, listOptions, func(obj runtime.Object) error {
		ph := obj.(*crd.ProviderHost)
		hostname := ph.Spec.Hostname

		// keep the hostname reserved: failing here would abort provider startup over one bad
		// resource, and skipping it would let another tenant claim the hostname
		lid, err := ph.Spec.LeaseID()
		if err != nil {
			lid = reservationLeaseID(ph)
			c.log.Error("invalid lease ID in ProviderHost, keeping its hostname reserved; fix or remove the resource and restart the provider",
				"hostname", hostname, "owner", lid.Owner, "err", err)
		}

		result = append(result, chostname.ActiveHostname{
			ID:       lid,
			Hostname: hostname,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// unrecoverableOwner reserves the hostnames of ProviderHosts whose owner cannot be recovered. It
// is a valid address, as hostname reservations require, that no tenant holds the key of, so the
// hostname is denied to every tenant. Reservations are loaded at startup and this one is never
// released, so it is lifted by fixing or removing the resource and restarting the provider.
func unrecoverableOwner() string {
	return sdk.AccAddress(make([]byte, 20)).String()
}

// reservationLeaseID returns the lease a ProviderHost reserves its hostname for when its spec does
// not yield a valid lease ID. Reservations compare owner, dseq and gseq only, so the owner is taken
// from the spec or, failing that, from the lease owner label; the provider address is kept as is.
func reservationLeaseID(ph *crd.ProviderHost) mtypes.LeaseID {
	lid := mtypes.LeaseID{
		Owner:    unrecoverableOwner(),
		DSeq:     ph.Spec.Dseq,
		GSeq:     ph.Spec.Gseq,
		OSeq:     ph.Spec.Oseq,
		Provider: ph.Spec.Provider,
	}

	for _, owner := range []string{ph.Spec.Owner, ph.Labels[builder.AkashLeaseOwnerLabelName]} {
		if _, err := sdk.AccAddressFromBech32(owner); err == nil {
			lid.Owner = owner
			break
		}
	}

	return lid
}
