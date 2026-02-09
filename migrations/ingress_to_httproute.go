package migrations

import (
	"context"
	"fmt"
	"math"
	"strconv"

	netv1 "k8s.io/api/networking/v1"
	kubeErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/tools/fromctx"
)

func init() {
	Register(NewIngressToHTTPRouteMigration())
}

var httpRouteGVRMigration = schema.GroupVersionResource{
	Group:    "gateway.networking.k8s.io",
	Version:  "v1",
	Resource: "httproutes",
}

func NewIngressToHTTPRouteMigration() Migration {
	return &ingressToHTTPRouteMigration{}
}

type ingressToHTTPRouteMigration struct{}

func (m *ingressToHTTPRouteMigration) Name() string {
	return "ingress-to-httproute-001"
}

func (m *ingressToHTTPRouteMigration) Description() string {
	return "Migrates existing Ingress resources to HTTPRoute resources for Gateway API mode"
}

func (m *ingressToHTTPRouteMigration) FromVersion() string {
	return "0.6.5"
}

func (m *ingressToHTTPRouteMigration) Run(ctx context.Context) error {
	kc, err := fromctx.KubeClientFromCtx(ctx)
	if err != nil {
		return fmt.Errorf("failed to get kube client: %w", err)
	}

	kubecfg, err := fromctx.KubeConfigFromCtx(ctx)
	if err != nil {
		return fmt.Errorf("failed to get kube config: %w", err)
	}

	dc, err := dynamic.NewForConfig(kubecfg)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	gatewayName := fromctx.GatewayNameFromCtx(ctx)
	gatewayNamespace := fromctx.GatewayNamespaceFromCtx(ctx)

	if gatewayName == "" || gatewayNamespace == "" {
		return nil
	}

	labelSelector := fmt.Sprintf("%s=true", builder.AkashManagedLabelName)

	ingresses, err := kc.NetworkingV1().Ingresses(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return fmt.Errorf("failed to list ingresses: %w", err)
	}

	for _, ingress := range ingresses.Items {
		if err := m.migrateIngress(ctx, dc, &ingress, gatewayName, gatewayNamespace); err != nil {
			return fmt.Errorf("failed to migrate ingress %s/%s: %w", ingress.Namespace, ingress.Name, err)
		}
	}

	return nil
}

func (m *ingressToHTTPRouteMigration) migrateIngress(
	ctx context.Context,
	dc dynamic.Interface,
	ingress *netv1.Ingress,
	gatewayName string,
	gatewayNamespace string,
) error {
	if len(ingress.Spec.Rules) == 0 {
		return nil
	}

	rule := ingress.Spec.Rules[0]
	if rule.HTTP == nil || len(rule.HTTP.Paths) == 0 {
		return nil
	}

	path := rule.HTTP.Paths[0]
	if path.Backend.Service == nil {
		return nil
	}

	routeName := ingress.Name
	ns := ingress.Namespace
	hostname := rule.Host
	serviceName := path.Backend.Service.Name
	servicePort := path.Backend.Service.Port.Number

	httpRoute := m.buildHTTPRoute(
		routeName,
		ingress.Labels,
		ingress.Annotations,
		gatewayName,
		gatewayNamespace,
		hostname,
		serviceName,
		servicePort,
	)

	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(httpRoute)
	if err != nil {
		return fmt.Errorf("failed to convert HTTPRoute to unstructured: %w", err)
	}
	u := &unstructured.Unstructured{Object: unstructuredObj}

	existing, err := dc.Resource(httpRouteGVRMigration).Namespace(ns).Get(ctx, routeName, metav1.GetOptions{})
	switch {
	case err == nil:
		u.SetResourceVersion(existing.GetResourceVersion())
		_, err = dc.Resource(httpRouteGVRMigration).Namespace(ns).Update(ctx, u, metav1.UpdateOptions{})
	case kubeErrors.IsNotFound(err):
		_, err = dc.Resource(httpRouteGVRMigration).Namespace(ns).Create(ctx, u, metav1.CreateOptions{})
	}

	return err
}

func (m *ingressToHTTPRouteMigration) buildHTTPRoute(
	name string,
	labels map[string]string,
	ingressAnnotations map[string]string,
	gatewayName string,
	gatewayNamespace string,
	hostname string,
	serviceName string,
	servicePort int32,
) *gatewayv1.HTTPRoute {
	annotations := m.convertAnnotations(ingressAnnotations)

	parentRefs := []gatewayv1.ParentReference{
		{
			Group:     ptrGroup(gatewayv1.GroupVersion.Group),
			Kind:      ptrKind("Gateway"),
			Namespace: (*gatewayv1.Namespace)(&gatewayNamespace),
			Name:      gatewayv1.ObjectName(gatewayName),
		},
	}

	pathType := gatewayv1.PathMatchPathPrefix
	backendPort := gatewayv1.PortNumber(servicePort)
	pathValue := "/"

	rules := []gatewayv1.HTTPRouteRule{
		{
			Matches: []gatewayv1.HTTPRouteMatch{
				{
					Path: &gatewayv1.HTTPPathMatch{
						Type:  &pathType,
						Value: &pathValue,
					},
				},
			},
			BackendRefs: []gatewayv1.HTTPBackendRef{
				{
					BackendRef: gatewayv1.BackendRef{
						BackendObjectReference: gatewayv1.BackendObjectReference{
							Name: gatewayv1.ObjectName(serviceName),
							Port: &backendPort,
						},
					},
				},
			},
		},
	}

	return &gatewayv1.HTTPRoute{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "gateway.networking.k8s.io/v1",
			Kind:       "HTTPRoute",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: parentRefs,
			},
			Hostnames: []gatewayv1.Hostname{gatewayv1.Hostname(hostname)},
			Rules:     rules,
		},
	}
}

func (m *ingressToHTTPRouteMigration) convertAnnotations(ingressAnnotations map[string]string) map[string]string {
	annotations := make(map[string]string)

	const ingressRoot = "nginx.ingress.kubernetes.io"
	const gatewayRoot = "nginx.org"

	readTimeout := m.getAnnotationInt(ingressAnnotations, ingressRoot+"/proxy-read-timeout", 60)
	sendTimeout := m.getAnnotationInt(ingressAnnotations, ingressRoot+"/proxy-send-timeout", 60)
	annotations[gatewayRoot+"/proxy-read-timeout"] = strconv.Itoa(readTimeout)
	annotations[gatewayRoot+"/proxy-send-timeout"] = strconv.Itoa(sendTimeout)

	bodySize := m.getAnnotationValue(ingressAnnotations, ingressRoot+"/proxy-body-size", "1m")
	annotations[gatewayRoot+"/client-max-body-size"] = bodySize

	nextTries := m.getAnnotationValue(ingressAnnotations, ingressRoot+"/proxy-next-upstream-tries", "3")
	annotations[gatewayRoot+"/proxy-next-upstream-tries"] = nextTries

	nextTimeout := m.getAnnotationValue(ingressAnnotations, ingressRoot+"/proxy-next-upstream-timeout", "0")
	if nextTimeout != "0" {
		nextTimeoutInt, err := strconv.Atoi(nextTimeout)
		if err == nil && nextTimeoutInt > 0 {
			annotations[gatewayRoot+"/proxy-next-upstream-timeout"] = fmt.Sprintf("%ds", nextTimeoutInt)
		}
	}

	nextUpstream := m.getAnnotationValue(ingressAnnotations, ingressRoot+"/proxy-next-upstream", "")
	if nextUpstream != "" {
		annotations[gatewayRoot+"/proxy-next-upstream"] = nextUpstream
	}

	return annotations
}

func (m *ingressToHTTPRouteMigration) getAnnotationValue(annotations map[string]string, key string, defaultValue string) string {
	if val, ok := annotations[key]; ok {
		return val
	}
	return defaultValue
}

func (m *ingressToHTTPRouteMigration) getAnnotationInt(annotations map[string]string, key string, defaultValue int) int {
	if val, ok := annotations[key]; ok {
		if intVal, err := strconv.Atoi(val); err == nil {
			return intVal
		}
		if floatVal, err := strconv.ParseFloat(val, 64); err == nil {
			return int(math.Ceil(floatVal))
		}
	}
	return defaultValue
}

func ptrGroup(s string) *gatewayv1.Group {
	g := gatewayv1.Group(s)
	return &g
}

func ptrKind(s string) *gatewayv1.Kind {
	k := gatewayv1.Kind(s)
	return &k
}
