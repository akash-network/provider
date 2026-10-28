package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"cosmossdk.io/log"

	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
)

const (
	backendTrafficPolicyAPIVersion = "gateway.envoyproxy.io/v1alpha1"
	backendTrafficPolicyKind       = "BackendTrafficPolicy"

	envoyControllerName = "gateway.envoyproxy.io/gatewayclass-controller"

	// idempotentRuleName names the rule that carries retries able to fire after the
	// backend has received the request. Only idempotent methods match it.
	idempotentRuleName     = "idempotent"
	idempotentPolicySuffix = "-idempotent"

	// RouteHashLabel selects every extension object of one route. Route names come
	// from hostnames and can exceed the 63-character label value limit, so the label
	// carries a digest and RouteNameAnnotation carries the readable name.
	RouteHashLabel      = "akash.network/route-hash"
	RouteNameAnnotation = "akash.network/route-name"

	// nginx defaults that apply today when a directive value is zero or unset.
	defaultUpstreamTimeoutMs uint32 = 60000

	// maxUpstreamTimeoutMs bounds tenant timeouts; values above it fall back to the
	// default rather than being passed through.
	maxUpstreamTimeoutMs uint32 = 24 * 60 * 60 * 1000

	// maxRetryAttempts caps next_tries (attempts including the first). Envoy has no
	// uncapped mode, so next_tries 0 (unlimited in nginx) also maps to this cap.
	maxRetryAttempts uint32 = 10

	maxObjectNameLen = 253
)

// next_cases tokens with a meaning of their own; the rest are status codes.
const (
	nextCaseOff           = "off"
	nextCaseError         = "error"
	nextCaseTimeout       = "timeout"
	nextCaseInvalidHeader = "invalid_header"
	nextCaseNonIdempotent = "non_idempotent"
)

// idempotentMethods are the methods the idempotent rule matches. This is a
// conservative allowlist: nginx only withholds POST, LOCK and PATCH from retries
// once they reached the upstream.
var idempotentMethods = []gatewayv1.HTTPMethod{
	gatewayv1.HTTPMethodGet,
	gatewayv1.HTTPMethodHead,
	gatewayv1.HTTPMethodOptions,
	gatewayv1.HTTPMethodPut,
	gatewayv1.HTTPMethodDelete,
	gatewayv1.HTTPMethodTrace,
}

// envoyGateway implements the Gateway API interface for Envoy Gateway.
// The http_options are applied as BackendTrafficPolicies targeting the route (and,
// when retries are split by method, its idempotent rule). A route or rule policy
// replaces the Gateway's policy rather than merging with it, so every policy carries
// all effective values.
type envoyGateway struct {
	log log.Logger
}

// NewEnvoyGateway creates a new Envoy Gateway provider.
func NewEnvoyGateway(logger log.Logger) GatewayProvider {
	return &envoyGateway{log: logger}
}

// Name returns the implementation identifier.
func (e *envoyGateway) Name() string {
	return "envoy"
}

func (e *envoyGateway) BuildAnnotations(_ chostname.ConnectToDeploymentDirective) map[string]string {
	return nil
}

// BuildHTTPRouteSpec builds the HTTPRoute spec. Rule 0 is the catch-all for every
// method. When retries are split, a second rule named "idempotent" matches the
// idempotent methods; it is more specific than the catch-all, so it takes precedence
// for those methods. Every rule disables the total request timeout: nginx has none,
// and Envoy's default of 15 s would cut long responses.
func (e *envoyGateway) BuildHTTPRouteSpec(
	gatewayName string,
	gatewayNamespace string,
	directive chostname.ConnectToDeploymentDirective,
	_ []*unstructured.Unstructured,
) gatewayv1.HTTPRouteSpec {
	parentRefs := []gatewayv1.ParentReference{
		{
			Group:     (*gatewayv1.Group)(&gatewayv1.GroupVersion.Group),
			Kind:      (*gatewayv1.Kind)(strPtr("Gateway")),
			Namespace: (*gatewayv1.Namespace)(&gatewayNamespace),
			Name:      gatewayv1.ObjectName(gatewayName),
		},
	}

	pathType := gatewayv1.PathMatchPathPrefix
	backendPort := gatewayv1.PortNumber(directive.ServicePort)
	noTimeout := gatewayv1.Duration("0s")

	backendRefs := []gatewayv1.HTTPBackendRef{
		{
			BackendRef: gatewayv1.BackendRef{
				BackendObjectReference: gatewayv1.BackendObjectReference{
					Name: gatewayv1.ObjectName(directive.ServiceName),
					Port: &backendPort,
				},
			},
		},
	}

	rules := []gatewayv1.HTTPRouteRule{
		{
			Matches: []gatewayv1.HTTPRouteMatch{
				{
					Path: &gatewayv1.HTTPPathMatch{
						Type:  &pathType,
						Value: strPtr("/"),
					},
				},
			},
			Timeouts:    &gatewayv1.HTTPRouteTimeouts{Request: &noTimeout},
			BackendRefs: backendRefs,
		},
	}

	if planRetries(directive.NextCases).split {
		matches := make([]gatewayv1.HTTPRouteMatch, 0, len(idempotentMethods))
		for i := range idempotentMethods {
			matches = append(matches, gatewayv1.HTTPRouteMatch{
				Path: &gatewayv1.HTTPPathMatch{
					Type:  &pathType,
					Value: strPtr("/"),
				},
				Method: &idempotentMethods[i],
			})
		}

		ruleName := gatewayv1.SectionName(idempotentRuleName)
		rules = append(rules, gatewayv1.HTTPRouteRule{
			Name:        &ruleName,
			Matches:     matches,
			Timeouts:    &gatewayv1.HTTPRouteTimeouts{Request: &noTimeout},
			BackendRefs: backendRefs,
		})
	}

	return gatewayv1.HTTPRouteSpec{
		CommonRouteSpec: gatewayv1.CommonRouteSpec{
			ParentRefs: parentRefs,
		},
		Hostnames: []gatewayv1.Hostname{gatewayv1.Hostname(directive.Hostname)},
		Rules:     rules,
	}
}

// BuildRouteExtensions returns the route's complete desired BackendTrafficPolicy set:
// one targeting the route, plus one targeting its idempotent rule when retries are
// split. Every directive carries options (the hostname operator fills defaults), so
// a route always gets at least the route-level policy.
func (e *envoyGateway) BuildRouteExtensions(namespace, routeName string, directive chostname.ConnectToDeploymentDirective) []*unstructured.Unstructured {
	// Rounded up to whole seconds, as the NGF renderer does today. Bounded by
	// maxUpstreamTimeoutMs, so the conversion cannot overflow.
	connectSec := msToSec(e.effectiveTimeout("proxy_connect_timeout", directive.ProxyConnectTimeout))
	connect := formatDuration(uint32(connectSec) * 1000) // nolint: gosec
	idle := formatDuration(max(
		e.effectiveTimeout("read_timeout", directive.ReadTimeout),
		e.effectiveTimeout("send_timeout", directive.SendTimeout),
	))
	retries := retryCount(directive.NextTries)
	plan := planRetries(directive.NextCases)

	routePolicy := plan.full
	if plan.split {
		routePolicy = plan.safe
	}

	exts := []*unstructured.Unstructured{
		backendTrafficPolicy(namespace, routeName, routeName, "", connect, idle, routePolicy, retries),
	}
	if plan.split {
		exts = append(exts, backendTrafficPolicy(namespace, PolicyName(routeName, idempotentPolicySuffix), routeName,
			idempotentRuleName, connect, idle, plan.full, retries))
	}

	for _, ext := range exts {
		labelRouteExtension(ext, routeName, directive.LeaseID)
	}

	return exts
}

// RouteFirst is true: a policy only gets status through the route's parent
// Gateway, so the route is published before its policies are awaited.
func (e *envoyGateway) RouteFirst() bool {
	return true
}

// ExtensionStatus reads Envoy Gateway's verdict from the policy's ancestor status
// for the given Gateway. Entries written by other controllers, or for other
// Gateways, are ignored.
func (e *envoyGateway) ExtensionStatus(obj *unstructured.Unstructured, gatewayNamespace, gatewayName string) ExtensionStatus {
	ancestors, _, _ := unstructured.NestedSlice(obj.Object, "status", "ancestors")
	for _, a := range ancestors {
		am, ok := a.(map[string]interface{})
		if !ok {
			continue
		}
		if controller, _, _ := unstructured.NestedString(am, "controllerName"); controller != envoyControllerName {
			continue
		}
		name, _, _ := unstructured.NestedString(am, "ancestorRef", "name")
		ns, _, _ := unstructured.NestedString(am, "ancestorRef", "namespace")
		if name != gatewayName || (ns != "" && ns != gatewayNamespace) {
			continue
		}
		conditions, _, _ := unstructured.NestedSlice(am, "conditions")
		if status, found := acceptedCondition(conditions, obj.GetGeneration()); found {
			return status
		}
	}
	return ExtensionStatus{State: ExtensionPending}
}

// effectiveTimeout resolves a directive timeout (milliseconds) to the value nginx
// applies today: zero means the nginx default, not "disabled". Out-of-range values
// fall back to the default.
func (e *envoyGateway) effectiveTimeout(field string, ms uint32) uint32 {
	if ms == 0 {
		return defaultUpstreamTimeoutMs
	}
	if ms > maxUpstreamTimeoutMs {
		e.log.Info("http option out of range, using default", "field", field, "value_ms", ms, "default_ms", defaultUpstreamTimeoutMs)
		return defaultUpstreamTimeoutMs
	}
	return ms
}

// retryPolicy is one rule's retry configuration. No triggers means no retries.
type retryPolicy struct {
	triggers    []string
	statusCodes []int64
}

// retryPlan splits the requested retries by safety. full carries every requested
// trigger; safe carries only those that fire before the request reaches the
// backend. When split is true, full applies to the idempotent rule and safe to
// every other method.
type retryPlan struct {
	split bool
	full  retryPolicy
	safe  retryPolicy
}

// planRetries maps next_cases to Envoy retry triggers. Tokens are validated against
// the same closed set as the NGF renderer. An empty or entirely invalid list means
// the nginx default "error timeout". "off" wins over every other token. "timeout"
// has no Envoy equivalent (the stream idle timeout ends the stream locally and never
// retries), and "invalid_header" has no trigger; both are dropped.
func planRetries(cases []string) retryPlan {
	var (
		errorSet      bool
		nonIdempotent bool
		off           bool
		valid         int
		codes         []int64
	)

	for _, c := range cases {
		tok, ok := normalizeNextCase(c)
		if !ok {
			continue
		}
		valid++

		switch tok {
		case nextCaseOff:
			off = true
		case nextCaseError:
			errorSet = true
		case nextCaseNonIdempotent:
			nonIdempotent = true
		case nextCaseTimeout, nextCaseInvalidHeader:
		default:
			code, err := strconv.ParseInt(strings.TrimPrefix(tok, "http_"), 10, 64)
			if err == nil && !containsCode(codes, code) {
				codes = append(codes, code)
			}
		}
	}

	if valid == 0 {
		errorSet = true
	}
	if off {
		return retryPlan{}
	}

	var full retryPolicy
	if errorSet {
		full.triggers = append(full.triggers, "connect-failure", "reset")
	}
	if len(codes) > 0 {
		sort.Slice(codes, func(i, j int) bool { return codes[i] < codes[j] })
		full.triggers = append(full.triggers, "retriable-status-codes")
		full.statusCodes = codes
	}

	// Every trigger above can fire after the backend received the request ("reset"
	// and status codes), so unless the tenant opted into non_idempotent, those
	// retries are limited to idempotent methods.
	if len(full.triggers) == 0 || nonIdempotent {
		return retryPlan{full: full}
	}

	var safe retryPolicy
	if errorSet {
		safe.triggers = []string{"connect-failure", "reset-before-request"}
	}

	return retryPlan{split: true, full: full, safe: safe}
}

func containsCode(codes []int64, code int64) bool {
	for _, c := range codes {
		if c == code {
			return true
		}
	}
	return false
}

// retryCount converts next_tries (attempts including the first) to Envoy's
// numRetries, in signed arithmetic. 0 (unlimited in nginx) and values above the cap
// map to the cap.
func retryCount(nextTries uint32) int64 {
	if nextTries == 0 || nextTries > maxRetryAttempts {
		return int64(maxRetryAttempts) - 1
	}
	return int64(nextTries) - 1
}

// backendTrafficPolicy renders one complete policy. sectionName targets a single
// rule of the route; empty targets the whole route.
func backendTrafficPolicy(
	namespace, name, routeName, sectionName string,
	connect, idle string,
	retry retryPolicy,
	retries int64,
) *unstructured.Unstructured {
	ref := gatewayv1.LocalPolicyTargetReferenceWithSectionName{
		LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
			Group: gatewayv1.Group(gatewayv1.GroupVersion.Group),
			Kind:  httpRouteKind,
			Name:  gatewayv1.ObjectName(routeName),
		},
	}
	if sectionName != "" {
		section := gatewayv1.SectionName(sectionName)
		ref.SectionName = &section
	}
	// A plain struct of strings always converts.
	targetRef, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&ref)

	spec := map[string]interface{}{
		"targetRefs": []interface{}{targetRef},
		"timeout": map[string]interface{}{
			"tcp": map[string]interface{}{
				"connectTimeout": connect,
			},
			"http": map[string]interface{}{
				"streamIdleTimeout": idle,
			},
		},
	}

	if len(retry.triggers) > 0 && retries > 0 {
		triggers := make([]interface{}, 0, len(retry.triggers))
		for _, t := range retry.triggers {
			triggers = append(triggers, t)
		}
		retryOn := map[string]interface{}{"triggers": triggers}
		if len(retry.statusCodes) > 0 {
			codes := make([]interface{}, 0, len(retry.statusCodes))
			for _, c := range retry.statusCodes {
				codes = append(codes, c)
			}
			retryOn["httpStatusCodes"] = codes
		}
		spec["retry"] = map[string]interface{}{
			"numRetries": retries,
			"retryOn":    retryOn,
		}
	}

	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": backendTrafficPolicyAPIVersion,
		"kind":       backendTrafficPolicyKind,
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
		"spec": spec,
	}}
}

// RouteHash returns the short digest of a route name used in RouteHashLabel.
func RouteHash(routeName string) string {
	sum := sha256.Sum256([]byte(routeName))
	return hex.EncodeToString(sum[:])[:16]
}

// PolicyName derives an extension object name from the route name and a suffix,
// truncating with a digest when the result would exceed the object name limit.
func PolicyName(routeName, suffix string) string {
	if len(routeName)+len(suffix) <= maxObjectNameLen {
		return routeName + suffix
	}

	hash := RouteHash(routeName)[:8]
	keep := maxObjectNameLen - len(suffix) - len(hash) - 1
	prefix := strings.TrimRight(routeName[:keep], ".-")

	return prefix + "-" + hash + suffix
}

// formatDuration renders milliseconds as a Gateway API duration (each component at
// most five digits), e.g. 90000 -> "1m30s".
func formatDuration(ms uint32) string {
	if ms == 0 {
		return "0s"
	}

	var b strings.Builder
	for _, unit := range []struct {
		suffix string
		size   uint32
	}{
		{"h", 3600000},
		{"m", 60000},
		{"s", 1000},
		{"ms", 1},
	} {
		if n := ms / unit.size; n > 0 {
			fmt.Fprintf(&b, "%d%s", n, unit.suffix)
			ms %= unit.size
		}
	}

	return b.String()
}
