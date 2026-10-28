package gateway

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/log"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	mtypes "pkg.akt.dev/go/node/market/v1"

	"github.com/akash-network/provider/cluster/kube/builder"
	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
)

// operatorDefaults mirrors the directive the hostname operator builds when the
// manifest sets no http_options.
func operatorDefaults() chostname.ConnectToDeploymentDirective {
	return chostname.ConnectToDeploymentDirective{
		Hostname:    "hello.localhost",
		LeaseID:     mtypes.LeaseID{Owner: "akash1owner", DSeq: 7, GSeq: 1, OSeq: 1, Provider: "akash1provider"},
		ServiceName: "web",
		ServicePort: 80,
		ReadTimeout: 60000,
		SendTimeout: 60000,
		NextTimeout: 60000,
		MaxBodySize: 1048576,
		NextTries:   3,
		NextCases:   []string{"error", "timeout"},
	}
}

func btpSpec(t *testing.T, btp *unstructured.Unstructured, path ...string) interface{} {
	t.Helper()
	v, found, err := unstructured.NestedFieldNoCopy(btp.Object, append([]string{"spec"}, path...)...)
	require.NoError(t, err)
	if !found {
		return nil
	}
	return v
}

// TestEnvoyDefaultsSplitRetries asserts the operator defaults render the split:
// a route policy with only pre-transmission retries and an idempotent-rule policy
// with the full set, both carrying every timeout.
func TestEnvoyDefaultsSplitRetries(t *testing.T) {
	gw := NewEnvoyGateway(log.NewNopLogger())
	exts := gw.BuildRouteExtensions("ns1", "hello.localhost", operatorDefaults())
	require.Len(t, exts, 2)

	route, idem := exts[0], exts[1]
	require.Equal(t, backendTrafficPolicyAPIVersion, route.GetAPIVersion())
	require.Equal(t, backendTrafficPolicyKind, route.GetKind())
	require.Equal(t, "hello.localhost", route.GetName())
	require.Equal(t, "hello.localhost-idempotent", idem.GetName())

	for _, btp := range exts {
		require.Equal(t, "ns1", btp.GetNamespace())
		require.Equal(t, "1m", btpSpec(t, btp, "timeout", "tcp", "connectTimeout"))
		require.Equal(t, "1m", btpSpec(t, btp, "timeout", "http", "streamIdleTimeout"))
		require.Equal(t, int64(2), btpSpec(t, btp, "retry", "numRetries"))
	}

	require.Equal(t, []interface{}{"connect-failure", "reset-before-request"}, btpSpec(t, route, "retry", "retryOn", "triggers"))
	require.Equal(t, []interface{}{"connect-failure", "reset"}, btpSpec(t, idem, "retry", "retryOn", "triggers"))

	refs := btpSpec(t, route, "targetRefs").([]interface{})
	require.Len(t, refs, 1)
	require.NotContains(t, refs[0], "sectionName")
	idemRefs := btpSpec(t, idem, "targetRefs").([]interface{})
	require.Equal(t, "idempotent", idemRefs[0].(map[string]interface{})["sectionName"])
	require.Equal(t, "hello.localhost", idemRefs[0].(map[string]interface{})["name"])
}

// TestEnvoyEffectiveTimeouts asserts zero means the nginx default, the idle timeout
// is the larger of read and send, connect rounds up to whole seconds as NGF does,
// and out-of-range values fall back to the default.
func TestEnvoyEffectiveTimeouts(t *testing.T) {
	gw := NewEnvoyGateway(log.NewNopLogger())

	cases := []struct {
		name                  string
		read, send, connect   uint32
		wantIdle, wantConnect string
	}{
		{"zero is the nginx default", 0, 0, 0, "1m", "1m"},
		{"idle is max of read and send", 20000, 0, 0, "1m", "1m"},
		{"explicit read wins", 90500, 1000, 0, "1m30s500ms", "1m"},
		{"connect rounds up", 0, 0, 1001, "1m", "2s"},
		{"out of range falls back", maxUpstreamTimeoutMs + 1, 0, maxUpstreamTimeoutMs + 1, "1m", "1m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := operatorDefaults()
			d.ReadTimeout, d.SendTimeout, d.ProxyConnectTimeout = tc.read, tc.send, tc.connect
			for _, btp := range gw.BuildRouteExtensions("ns1", d.Hostname, d) {
				require.Equal(t, tc.wantIdle, btpSpec(t, btp, "timeout", "http", "streamIdleTimeout"))
				require.Equal(t, tc.wantConnect, btpSpec(t, btp, "timeout", "tcp", "connectTimeout"))
			}
		})
	}
}

// TestEnvoyRetryCount asserts next_tries maps to numRetries = tries - 1, with 0
// (unlimited in nginx) and large values capped at 10 attempts, and a single try
// rendering no retry block.
func TestEnvoyRetryCount(t *testing.T) {
	require.Equal(t, int64(9), retryCount(0))
	require.Equal(t, int64(0), retryCount(1))
	require.Equal(t, int64(2), retryCount(3))
	require.Equal(t, int64(9), retryCount(10))
	require.Equal(t, int64(9), retryCount(4294967295))

	gw := NewEnvoyGateway(log.NewNopLogger())
	d := operatorDefaults()
	d.NextTries = 1
	for _, btp := range gw.BuildRouteExtensions("ns1", d.Hostname, d) {
		require.Nil(t, btpSpec(t, btp, "retry"))
	}
}

// TestPlanRetries covers token handling: off wins, empty and all-invalid lists mean
// the nginx default, timeout and invalid_header add nothing, status codes force the
// split, a status-only policy adds no connection retries to the catch-all, and
// non_idempotent removes the split.
func TestPlanRetries(t *testing.T) {
	cases := []struct {
		name  string
		cases []string
		want  retryPlan
	}{
		{"off wins", []string{"error", "off", "500"}, retryPlan{}},
		{"empty means error timeout", nil, retryPlan{
			split: true,
			full:  retryPolicy{triggers: []string{"connect-failure", "reset"}},
			safe:  retryPolicy{triggers: []string{"connect-failure", "reset-before-request"}},
		}},
		{"all invalid means error timeout", []string{"; drop;", "bogus"}, retryPlan{
			split: true,
			full:  retryPolicy{triggers: []string{"connect-failure", "reset"}},
			safe:  retryPolicy{triggers: []string{"connect-failure", "reset-before-request"}},
		}},
		{"timeout alone adds nothing", []string{"timeout", "invalid_header"}, retryPlan{}},
		{"status only", []string{"502", "http_503", "502"}, retryPlan{
			split: true,
			full:  retryPolicy{triggers: []string{"retriable-status-codes"}, statusCodes: []int64{502, 503}},
		}},
		{"non_idempotent has no split", []string{"error", "429", "non_idempotent"}, retryPlan{
			full: retryPolicy{triggers: []string{"connect-failure", "reset", "retriable-status-codes"}, statusCodes: []int64{429}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, planRetries(tc.cases))
		})
	}
}

// TestEnvoyNonIdempotentSinglePolicy asserts opting into non_idempotent renders one
// route policy with the full trigger set and a single rule.
func TestEnvoyNonIdempotentSinglePolicy(t *testing.T) {
	gw := NewEnvoyGateway(log.NewNopLogger())
	d := operatorDefaults()
	d.NextCases = []string{"error", "http_502", "non_idempotent"}

	exts := gw.BuildRouteExtensions("ns1", d.Hostname, d)
	require.Len(t, exts, 1)
	require.Equal(t, []interface{}{"connect-failure", "reset", "retriable-status-codes"}, btpSpec(t, exts[0], "retry", "retryOn", "triggers"))
	require.Equal(t, []interface{}{int64(502)}, btpSpec(t, exts[0], "retry", "retryOn", "httpStatusCodes"))

	spec := gw.BuildHTTPRouteSpec("akash-gateway", "akash-gateway", d, exts)
	require.Len(t, spec.Rules, 1)
}

// TestEnvoyRetriesOffStillCarriesTimeouts asserts a route with retries off still
// gets a complete policy, so it never falls back to Envoy's 10 s connect timeout.
func TestEnvoyRetriesOffStillCarriesTimeouts(t *testing.T) {
	gw := NewEnvoyGateway(log.NewNopLogger())
	d := operatorDefaults()
	d.NextCases = []string{"off"}

	exts := gw.BuildRouteExtensions("ns1", d.Hostname, d)
	require.Len(t, exts, 1)
	require.Equal(t, "1m", btpSpec(t, exts[0], "timeout", "tcp", "connectTimeout"))
	require.Nil(t, btpSpec(t, exts[0], "retry"))
}

// TestEnvoyHTTPRouteSpec asserts the route has no ExtensionRef, disables the total
// request timeout on every rule, and adds the idempotent rule only when split.
func TestEnvoyHTTPRouteSpec(t *testing.T) {
	gw := NewEnvoyGateway(log.NewNopLogger())
	d := operatorDefaults()

	spec := gw.BuildHTTPRouteSpec("akash-gateway", "gwns", d, nil)
	require.Equal(t, gatewayv1.ObjectName("akash-gateway"), spec.ParentRefs[0].Name)
	require.Equal(t, gatewayv1.Namespace("gwns"), *spec.ParentRefs[0].Namespace)
	require.Equal(t, []gatewayv1.Hostname{"hello.localhost"}, spec.Hostnames)
	require.Len(t, spec.Rules, 2)

	for _, rule := range spec.Rules {
		require.Empty(t, rule.Filters)
		require.NotNil(t, rule.Timeouts)
		require.Equal(t, gatewayv1.Duration("0s"), *rule.Timeouts.Request)
		require.Equal(t, gatewayv1.ObjectName("web"), rule.BackendRefs[0].Name)
		require.Equal(t, gatewayv1.PortNumber(80), *rule.BackendRefs[0].Port)
	}

	require.Nil(t, spec.Rules[0].Name)
	require.Len(t, spec.Rules[0].Matches, 1)
	require.Nil(t, spec.Rules[0].Matches[0].Method)

	require.Equal(t, gatewayv1.SectionName("idempotent"), *spec.Rules[1].Name)
	methods := make([]gatewayv1.HTTPMethod, 0, len(spec.Rules[1].Matches))
	for _, m := range spec.Rules[1].Matches {
		require.Equal(t, "/", *m.Path.Value)
		methods = append(methods, *m.Method)
	}
	require.Equal(t, idempotentMethods, methods)

	d.NextCases = []string{"off"}
	require.Len(t, gw.BuildHTTPRouteSpec("akash-gateway", "gwns", d, nil).Rules, 1)
}

// TestEnvoyPolicyLabels asserts every policy carries the management and lease
// labels, a route digest label within the label value limit, and the readable route
// name as an annotation.
func TestEnvoyPolicyLabels(t *testing.T) {
	gw := NewEnvoyGateway(log.NewNopLogger())
	d := operatorDefaults()
	long := strings.Repeat("a", 60) + "." + strings.Repeat("b", 60) + ".ingress.example.com"
	d.Hostname = long

	for _, btp := range gw.BuildRouteExtensions("ns1", long, d) {
		labels := btp.GetLabels()
		require.Equal(t, "true", labels[builder.AkashManagedLabelName])
		require.Equal(t, "akash1owner", labels[builder.AkashLeaseOwnerLabelName])
		require.Equal(t, "7", labels[builder.AkashLeaseDSeqLabelName])
		require.Equal(t, RouteHash(long), labels[RouteHashLabel])
		require.Empty(t, validation.IsValidLabelValue(labels[RouteHashLabel]))
		require.Equal(t, long, btp.GetAnnotations()[RouteNameAnnotation])
	}
}

// TestPolicyName asserts derived names stay valid object names at the length limit
// and stay distinct for routes that share a long prefix.
func TestPolicyName(t *testing.T) {
	require.Equal(t, "hello.localhost-idempotent", PolicyName("hello.localhost", idempotentPolicySuffix))

	base := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
	other := base[:len(base)-1] + "e"
	require.Len(t, base, maxObjectNameLen)

	n1 := PolicyName(base, idempotentPolicySuffix)
	n2 := PolicyName(other, idempotentPolicySuffix)
	require.LessOrEqual(t, len(n1), maxObjectNameLen)
	require.Empty(t, validation.IsDNS1123Subdomain(n1))
	require.True(t, strings.HasSuffix(n1, idempotentPolicySuffix))
	require.NotEqual(t, n1, n2)
}

// TestFormatDuration asserts milliseconds render as valid Gateway API durations.
func TestFormatDuration(t *testing.T) {
	require.Equal(t, "0s", formatDuration(0))
	require.Equal(t, "1m", formatDuration(60000))
	require.Equal(t, "1m30s500ms", formatDuration(90500))
	require.Equal(t, "24h", formatDuration(maxUpstreamTimeoutMs))
	require.Equal(t, "1ms", formatDuration(1))
}

// TestRegistryEnvoy asserts the envoy provider is registered.
func TestRegistryEnvoy(t *testing.T) {
	p, err := GetProvider("envoy", log.NewNopLogger())
	require.NoError(t, err)
	require.Equal(t, "envoy", p.Name())
	require.Contains(t, defaultRegistry.SupportedNames(), "envoy")
}
