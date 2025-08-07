package builder

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	manitypes "github.com/akash-network/akash-api/go/manifest/v2beta2"
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
func (b *netPol) Create() ([]*netv1.NetworkPolicy, error) { // nolint:golint,unparam
	if !b.settings.NetworkPoliciesEnabled {
		return []*netv1.NetworkPolicy{}, nil
	}

	const ingressLabelName = "app.kubernetes.io/name"
	const ingressLabelValue = "ingress-nginx"

	//extract ownerID from LeaseID
	ownerID := b.deployment.LeaseID().Owner

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

	//allow-same-owner: enable cross-namespace traffic between namespaces owned by same owner
	allowSameOwner := &netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      akashAllowSameOwner,
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
				{
					From: []netv1.NetworkPolicyPeer{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									AkashLeaseOwnerLabelName: ownerID,
								},
							},
						},
					},
				},
			},
			Egress: []netv1.NetworkPolicyEgressRule{
				{
					To: []netv1.NetworkPolicyPeer{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									AkashLeaseOwnerLabelName: ownerID,
								},
							},
						},
					},
				},
			},
		},
	}

	result = append(result, allowSameOwner)

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
	}

	return result, nil
}

// Update a single NetworkPolicy with correct labels.
func (b *netPol) Update(obj *netv1.NetworkPolicy) (*netv1.NetworkPolicy, error) { // nolint:golint,unparam
	uobj := obj.DeepCopy()

	uobj.Labels = updateAkashLabels(obj.Labels, b.labels())

	return uobj, nil
}
