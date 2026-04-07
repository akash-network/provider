package builder

import (
	"fmt"
	"net"

	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	manitypes "pkg.akt.dev/go/manifest/v2beta3"
)

type NetPol interface {
	builderBase
	Create() ([]*netv1.NetworkPolicy, error)
	Update(obj *netv1.NetworkPolicy) (*netv1.NetworkPolicy, error)
}

type netPol struct {
	builder
}

var _ NetPol = (*netPol)(nil)

func BuildNetPol(settings Settings, deployment IClusterDeployment) NetPol {
	return &netPol{builder: builder{settings: settings, deployment: deployment}}
}

// Create a set of NetworkPolicies to restrict the ingress traffic to a Tenant's
// Deployment namespace.

func (b *netPol) Create() ([]*netv1.NetworkPolicy, error) { // nolint:unparam
	if !b.settings.NetworkPoliciesEnabled {
		return []*netv1.NetworkPolicy{}, nil
	}

	const ingressLabelName = "app.kubernetes.io/name"
	const ingressLabelValue = "ingress-nginx"

	result := []*netv1.NetworkPolicy{
		{

			ObjectMeta: metav1.ObjectMeta{
				Name:      akashDeploymentPolicyName,
				Labels:    b.labels(),
				Namespace: LidNS(b.deployment.LeaseID()),
			},
			Spec: netv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{},
				PolicyTypes: []netv1.PolicyType{
					netv1.PolicyTypeIngress,
					netv1.PolicyTypeEgress,
				},
				Ingress: []netv1.NetworkPolicyIngressRule{
					{ // Allow Network Connections from same Namespace
						From: []netv1.NetworkPolicyPeer{
							{
								NamespaceSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										akashNetworkNamespace: LidNS(b.deployment.LeaseID()),
									},
								},
							},
						},
					},
					{ // Allow Network Connections from NGINX ingress controller
						From: []netv1.NetworkPolicyPeer{
							{
								NamespaceSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										ingressLabelName: ingressLabelValue,
									},
								},
								PodSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										ingressLabelName: ingressLabelValue,
									},
								},
							},
						},
					},
				},
				Egress: []netv1.NetworkPolicyEgressRule{
					{ // Allow Network Connections to same Namespace
						To: []netv1.NetworkPolicyPeer{
							{
								NamespaceSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										akashNetworkNamespace: LidNS(b.deployment.LeaseID()),
									},
								},
							},
						},
					},
					{ // Allow DNS to internal server
						Ports: []netv1.NetworkPolicyPort{
							{
								Protocol: &udpProtocol,
								Port:     &dnsPort,
							},
							{
								Protocol: &tcpProtocol,
								Port:     &dnsPort,
							},
						},
						To: []netv1.NetworkPolicyPeer{
							{
								PodSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"k8s-app": "kube-dns",
									},
								},
								NamespaceSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"kubernetes.io/metadata.name": "kube-system",
									},
								},
							},
						},
					},
					{ // Allow access to IPV4 Public addresses only
						To: []netv1.NetworkPolicyPeer{
							{
								PodSelector:       nil,
								NamespaceSelector: nil,
								IPBlock: &netv1.IPBlock{
									CIDR: "0.0.0.0/0",
									Except: []string{
										"10.0.0.0/8",
										"192.168.0.0/16",
										"172.16.0.0/12",
									},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, service := range b.deployment.ManifestGroup().Services {
		// find all the ports that are exposed directly
		ports := make([]netv1.NetworkPolicyPort, 0)
		portsWithIP := make([]netv1.NetworkPolicyPort, 0)

		for _, expose := range service.Expose {
			portAsIntStr := intstr.FromInt(int(expose.Port))

			var exposeProto corev1.Protocol
			switch expose.Proto {
			case manitypes.TCP:
				exposeProto = corev1.ProtocolTCP
			case manitypes.UDP:
				exposeProto = corev1.ProtocolUDP
			}

			entry := netv1.NetworkPolicyPort{
				Port:     &portAsIntStr,
				Protocol: &exposeProto,
			}

			if len(expose.IP) != 0 {
				portsWithIP = append(portsWithIP, entry)
			}

			if !expose.Global || expose.IsIngress() {
				continue
			}

			ports = append(ports, entry)
		}

		serviceName := service.Name
		// If no ports are found, skip this service
		if len(ports) != 0 {
			// Make a network policy just to open these ports to incoming traffic
			policyName := fmt.Sprintf("akash-%s-np", serviceName)
			policy := netv1.NetworkPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Labels:    b.labels(),
					Name:      policyName,
					Namespace: LidNS(b.deployment.LeaseID()),
				},
				Spec: netv1.NetworkPolicySpec{
					Ingress: []netv1.NetworkPolicyIngressRule{
						{
							Ports: ports,
						},
					},
					PodSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							AkashManifestServiceLabelName: serviceName,
						},
					},
					PolicyTypes: []netv1.PolicyType{
						netv1.PolicyTypeIngress,
					},
				},
			}
			result = append(result, &policy)
		}

		if len(portsWithIP) != 0 {
			// Make a network policy just to open these ports to incoming traffic from outside the cluster
			policyName := fmt.Sprintf("akash-%s-ip", serviceName)
			policy := netv1.NetworkPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Labels:    b.labels(),
					Name:      policyName,
					Namespace: LidNS(b.deployment.LeaseID()),
				},
				Spec: netv1.NetworkPolicySpec{
					Ingress: []netv1.NetworkPolicyIngressRule{
						{
							From: []netv1.NetworkPolicyPeer{
								{
									IPBlock: &netv1.IPBlock{
										CIDR: "0.0.0.0/0",
									},
								},
							},
							Ports: portsWithIP,
						},
					},
					PodSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							AkashManifestServiceLabelName: serviceName,
						},
					},
					PolicyTypes: []netv1.PolicyType{
						netv1.PolicyTypeIngress,
					},
				},
			}
			result = append(result, &policy)
		}

		// Allow egress to all Kubernetes API server backends for services with
		// permissions. HA control planes have multiple backends; all must be
		// allowed because CNIs like Calico evaluate egress rules after DNAT.
		if len(b.settings.APIServerEndpoints) > 0 && serviceHasReadPermissions(service) {
			peers := make([]netv1.NetworkPolicyPeer, 0, len(b.settings.APIServerEndpoints))
			ports := make([]netv1.NetworkPolicyPort, 0, len(b.settings.APIServerEndpoints))
			seen := make(map[int32]struct{})

			for _, ep := range b.settings.APIServerEndpoints {
				peers = append(peers, netv1.NetworkPolicyPeer{
					IPBlock: &netv1.IPBlock{
						CIDR: hostCIDR(ep.IP),
					},
				})
				p := int32(ep.Port) //nolint:gosec // port values are always in range 1-65535
				if _, ok := seen[p]; !ok {
					seen[p] = struct{}{}
					apiServerPort := intstr.FromInt32(p)
					ports = append(ports, netv1.NetworkPolicyPort{
						Protocol: &tcpProtocol,
						Port:     &apiServerPort,
					})
				}
			}

			policyName := fmt.Sprintf("akash-apiserver-%s", serviceName)
			policy := netv1.NetworkPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Labels:    b.labels(),
					Name:      policyName,
					Namespace: LidNS(b.deployment.LeaseID()),
				},
				Spec: netv1.NetworkPolicySpec{
					PodSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							AkashManifestServiceLabelName: serviceName,
						},
					},
					PolicyTypes: []netv1.PolicyType{
						netv1.PolicyTypeEgress,
					},
					Egress: []netv1.NetworkPolicyEgressRule{
						{
							To:    peers,
							Ports: ports,
						},
					},
				},
			}
			result = append(result, &policy)
		}
	}

	return result, nil
}

// hostCIDR returns a single-host CIDR string for the given IP,
// using /32 for IPv4 and /128 for IPv6.
func hostCIDR(ip net.IP) string {
	if ip.To4() != nil {
		return ip.String() + "/32"
	}
	return ip.String() + "/128"
}

func serviceHasReadPermissions(service manitypes.Service) bool {
	return service.Params != nil && service.Params.Permissions != nil && len(service.Params.Permissions.Read) > 0
}

// Update a single NetworkPolicy with correct labels.
func (b *netPol) Update(obj *netv1.NetworkPolicy) (*netv1.NetworkPolicy, error) { // nolint:unparam
	uobj := obj.DeepCopy()

	uobj.Labels = updateAkashLabels(obj.Labels, b.labels())

	return uobj, nil
}
