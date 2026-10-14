package kube

import (
	"context"
	"errors"
	"testing"

	"cosmossdk.io/log"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/akash-network/provider/cluster/kube/builder"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
)

func TestDetectTEEPlatform(t *testing.T) {
	node := func(name string, labels map[string]string) runtime.Object {
		labels[builder.AkashManagedLabelName] = builder.ValTrue
		return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
	}
	unmanagedNode := func(name string, labels map[string]string) runtime.Object {
		return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
	}

	tests := []struct {
		name  string
		nodes []runtime.Object
		want  ctypes.TEEPlatform
	}{
		{name: "none", want: ctypes.TEEPlatformNone},
		{name: "SNP only", nodes: []runtime.Object{node("snp", map[string]string{amdSNPLabelKey: "true"})}, want: ctypes.TEEPlatformSNP},
		{name: "TDX only", nodes: []runtime.Object{node("tdx", map[string]string{intelTDXLabelKey: "true"})}, want: ctypes.TEEPlatformTDX},
		{name: "homogeneous across nodes", nodes: []runtime.Object{
			node("snp-a", map[string]string{amdSNPLabelKey: "true"}),
			node("snp-b", map[string]string{amdSNPLabelKey: "true"}),
		}, want: ctypes.TEEPlatformSNP},
		{name: "unmanaged opposite platform ignored", nodes: []runtime.Object{
			node("snp", map[string]string{amdSNPLabelKey: "true"}),
			unmanagedNode("tdx", map[string]string{intelTDXLabelKey: "true"}),
		}, want: ctypes.TEEPlatformSNP},
		{name: "mixed managed nodes unresolved", nodes: []runtime.Object{
			node("snp", map[string]string{amdSNPLabelKey: "true"}),
			node("tdx", map[string]string{intelTDXLabelKey: "true"}),
		}, want: ctypes.TEEPlatformNone},
		{name: "both labels on one node", nodes: []runtime.Object{
			node("mixed", map[string]string{amdSNPLabelKey: "true", intelTDXLabelKey: "true"}),
		}, want: ctypes.TEEPlatformNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &client{kc: fake.NewClientset(tt.nodes...), log: log.NewNopLogger()}
			got := c.DetectTEEPlatform(context.Background())
			require.Equal(t, tt.want, got)
		})
	}
}

func TestDetectTEEPlatformPropagatesListFailure(t *testing.T) {
	kc := fake.NewClientset()
	wantErr := errors.New("nodes unavailable")
	kc.PrependReactor("list", "nodes", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, wantErr
	})

	c := &client{kc: kc, log: log.NewNopLogger()}
	got := c.DetectTEEPlatform(context.Background())
	require.Equal(t, ctypes.TEEPlatformNone, got)
}
