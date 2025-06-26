package builder

import (
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func (b *netPol) Create() ([]*netv1.NetworkPolicy, error) {
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
					{
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
					{
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
					{
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
					{
						Ports: []netv1.NetworkPolicyPort{
							{Protocol: &udpProtocol, Port: &dnsPort},
							{Protocol: &tcpProtocol, Port: &dnsPort},
						},
						To: []netv1.NetworkPolicyPeer{
							{
								PodSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{"k8s-app": "kube-dns"},
								},
								NamespaceSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"},
								},
							},
						},
					},
					{
						To: []netv1.NetworkPolicyPeer{
							{
								IPBlock: &netv1.IPBlock{
									CIDR:   "0.0.0.0/0",
									Except: []string{"10.0.0.0/8", "192.168.0.0/16", "172.16.0.0/12"},
								},
							},
						},
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "allow-same-owner",
				Labels:    b.labels(),
				Namespace: LidNS(b.deployment.LeaseID()),
			},
			Spec: netv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{},
				Ingress: []netv1.NetworkPolicyIngressRule{{
					From: []netv1.NetworkPolicyPeer{{
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								AkashLeaseOwnerLabelName: b.deployment.LeaseID().Owner,
							},
						},
					}},
				}},
				Egress: []netv1.NetworkPolicyEgressRule{{
					To: []netv1.NetworkPolicyPeer{{
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								AkashLeaseOwnerLabelName: b.deployment.LeaseID().Owner,
							},
						},
					}},
				}},
				PolicyTypes: []netv1.PolicyType{
					netv1.PolicyTypeIngress,
					netv1.PolicyTypeEgress,
				},
			},
		},
	}

	// existing service-specific policy generation code...

	// [truncated here to focus on the same-owner logic]

	return result, nil
}

func (b *netPol) Update(obj *netv1.NetworkPolicy) (*netv1.NetworkPolicy, error) {
	uobj := obj.DeepCopy()
	uobj.Labels = updateAkashLabels(obj.Labels, b.labels())
	return uobj, nil
}
