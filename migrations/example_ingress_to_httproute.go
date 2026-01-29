package migrations

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/tools/fromctx"
)

func init() {
	Register(NewIngressToHTTPRouteMigration())
}

// Note: This migration is a placeholder and does not actually create HTTPRoute resources.
func NewIngressToHTTPRouteMigration() Migration {
	return &ingressToHTTPRouteMigration{}
}

type ingressToHTTPRouteMigration struct{}

func (m *ingressToHTTPRouteMigration) Name() string {
	return "ingress-to-httproute-001"
}

func (m *ingressToHTTPRouteMigration) Description() string {
	return "Example migration: Migrates Ingress resources to HTTPRoute resources (placeholder implementation)"
}

func (m *ingressToHTTPRouteMigration) FromVersion() string {
	return "0.10.5"
}

func (m *ingressToHTTPRouteMigration) Run(ctx context.Context) error {
	kc, err := fromctx.KubeClientFromCtx(ctx)
	if err != nil {
		return err
	}

	selector := labels.Set{
		builder.AkashManagedLabelName: "true",
	}.AsSelector()

	ingresses, err := kc.NetworkingV1().Ingresses(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return fmt.Errorf("failed to list ingresses: %w", err)
	}

	for _, ingress := range ingresses.Items {
		// TODO: Check if HTTPRoute already exists for this Ingress
		// If it does, skip this ingress as it's already been migrated
		// For now, this is a placeholder that logs the migration would happen
		_ = ingress
	}

	return nil
}
