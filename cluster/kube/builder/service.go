package builder

import (
	"fmt"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	manitypes "github.com/akash-network/akash-api/go/manifest/v2beta2"
)

type Service interface {
	workloadBase
	Create() (*corev1.Service, error)
	Update(obj *corev1.Service) (*corev1.Service, error)
	Any() bool
}

type service struct {
	*Workload
	requireNodePort bool
	portAllocator   ServicePortAllocator
}

var _ Service = (*service)(nil)

func BuildService(workload *Workload, requireNodePort bool, allocator ServicePortAllocator) (Service, error) {
	if requireNodePort && allocator == nil {
		return nil, fmt.Errorf("ServicePortAllocator is required when requireNodePort is true")
	}

	ss := &service{
		Workload:        workload,
		requireNodePort: requireNodePort,
		portAllocator:   allocator,
	}

	ss.Workload.log = ss.Workload.log.With("object", "service", "service-name", ss.deployment.ManifestGroup().Services[ss.serviceIdx].Name)

	return ss, nil
}

//go:generate mockery --name ServicePortAllocator --case underscore --with-expecter

// ServicePortAllocator is a minimal interface for allocating ports during service building.
// This is a builder-specific abstraction that keeps the service builder decoupled
// from the full cluster.PortManager interface.
//
// Implementation: leasePortAllocator in cluster/kube/client.go adapts the unified
// cluster.PortManager to this simple interface by binding it to a specific leaseID.
//
// Why not use PortManager directly?
// - Service builder only needs port allocation, not order management or cleanup
// - This interface is simpler and focused on the builder's specific needs
// - It maintains separation of concerns between builder and cluster layers
type ServicePortAllocator interface {
	AllocatePorts(serviceName string, count int) []int32
}

func (b *service) Name() string {
	basename := b.Workload.Name()
	if b.requireNodePort {
		return makeGlobalServiceNameFromBasename(basename)
	}
	return basename
}

func (b *service) workloadServiceType() corev1.ServiceType {
	if b.requireNodePort {
		return corev1.ServiceTypeNodePort
	}
	return corev1.ServiceTypeClusterIP
}

func (b *service) Create() (*corev1.Service, error) { // nolint:golint,unparam
	ports, err := b.ports()
	if err != nil {
		return nil, err
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:   b.Name(),
			Labels: b.labels(),
		},
		Spec: corev1.ServiceSpec{
			Type:     b.workloadServiceType(),
			Selector: b.selectorLabels(),
			Ports:    ports,
		},
	}

	return svc, nil
}

func (b *service) Update(obj *corev1.Service) (*corev1.Service, error) { // nolint:golint,unparam
	uobj := obj.DeepCopy()

	uobj.Labels = updateAkashLabels(obj.Labels, b.labels())
	uobj.Spec.Selector = b.selectorLabels()
	ports, err := b.ports()
	if err != nil {
		return nil, err
	}

	// retain provisioned NodePort values
	if b.requireNodePort {
		// for each newly-calculated port
		for i, port := range ports {

			// if there is a current (in-kube) port defined
			// with the same specified values
			for _, curport := range obj.Spec.Ports {
				if curport.Name == port.Name &&
					curport.Port == port.Port &&
					curport.TargetPort.IntValue() == port.TargetPort.IntValue() &&
					curport.Protocol == port.Protocol {

					// re-use current port
					ports[i] = curport
				}
			}
		}
	}

	uobj.Spec.Ports = ports

	return uobj, nil
}

func (b *service) Any() bool {
	service := &b.deployment.ManifestGroup().Services[b.serviceIdx]

	for _, expose := range service.Expose {
		if b.requireNodePort && expose.IsIngress() {
			continue
		}

		if !b.requireNodePort && expose.IsIngress() {
			return true
		}

		if expose.Global == b.requireNodePort {
			return true
		}
	}
	return false
}

var errUnsupportedProtocol = errors.New("Unsupported protocol for service")
var errInvalidServiceBuilder = errors.New("service builder invalid")

func (b *service) ports() ([]corev1.ServicePort, error) {
	service := &b.deployment.ManifestGroup().Services[b.serviceIdx]

	ports := make([]corev1.ServicePort, 0, len(service.Expose))

	// Pre-allocate NodePorts for this service if using port allocator
	var allocatedPorts []int32
	if b.requireNodePort {
		// Count how many unique NodePorts we need using a separate map
		countedPorts := make(map[int32]struct{})
		nodePortCount := 0
		for _, expose := range service.Expose {
			if expose.Global && !expose.IsIngress() {
				externalPort := expose.GetExternalPort()
				if _, counted := countedPorts[externalPort]; !counted {
					nodePortCount++
					countedPorts[externalPort] = struct{}{} // Mark as counted
				}
			}
		}

		// Allocate all needed ports at once
		if nodePortCount > 0 {
			allocatedPorts = b.portAllocator.AllocatePorts(service.Name, nodePortCount)
		}
	}

	// Separate map for duplicate detection during port creation
	portsAdded := make(map[int32]struct{})

	allocatedIndex := 0
	for i, expose := range service.Expose {
		if expose.Global == b.requireNodePort || (!b.requireNodePort && expose.IsIngress()) {
			if b.requireNodePort && expose.IsIngress() {
				continue
			}

			var exposeProtocol corev1.Protocol
			switch expose.Proto {
			case manitypes.TCP:
				exposeProtocol = corev1.ProtocolTCP
			case manitypes.UDP:
				exposeProtocol = corev1.ProtocolUDP
			default:
				return nil, errUnsupportedProtocol
			}
			externalPort := expose.GetExternalPort()
			_, added := portsAdded[externalPort]
			if !added {
				portsAdded[externalPort] = struct{}{}
				sp := corev1.ServicePort{
					Name:       fmt.Sprintf("%d-%d", i, int(externalPort)),
					Port:       externalPort,
					TargetPort: intstr.FromInt(int(expose.Port)),
					Protocol:   exposeProtocol,
				}

				// Assign NodePort using allocator
				if b.requireNodePort && len(allocatedPorts) > allocatedIndex {
					sp.NodePort = allocatedPorts[allocatedIndex]
					allocatedIndex++
				}

				ports = append(ports, sp)
			}
		}
	}

	if len(ports) == 0 {
		b.log.Debug("provider/cluster/kube/builder: created 0 ports", "requireNodePort", b.requireNodePort, "serviceExpose", service.Expose)
		return nil, errInvalidServiceBuilder
	}

	return ports, nil
}
