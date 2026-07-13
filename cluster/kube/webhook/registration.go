package webhook

import (
	"context"
	"fmt"

	"cosmossdk.io/log"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/akash-network/provider/cluster/kube/builder"
)

const (
	webhookName       = "attestation-sidecar-injector.akash.network"
	webhookConfigName = "akash-attestation-sidecar-injector"
)

// RegisterWebhookConfiguration creates or updates the MutatingWebhookConfiguration
// in the cluster. This tells the K8s API server to send pod CREATE requests
// (in Akash-managed namespaces) to our webhook for sidecar injection.
//
// The caBundle must be PEM-encoded. When webhookURL is non-empty, it registers
// with a URL endpoint (for local dev). Otherwise it uses a K8s Service reference.
func RegisterWebhookConfiguration(ctx context.Context, kc kubernetes.Interface, log log.Logger, serviceName, serviceNamespace string, caBundle []byte, webhookPort int32, webhookURL string) error {
	failPolicy := admissionregistrationv1.Fail // fail-closed
	sideEffects := admissionregistrationv1.SideEffectClassNone
	matchPolicy := admissionregistrationv1.Equivalent
	timeoutSec := int32(5)

	path := "/mutate"

	var clientConfig admissionregistrationv1.WebhookClientConfig
	if webhookURL != "" {
		// Local dev: provider runs outside the cluster, use URL endpoint
		url := webhookURL + path
		clientConfig = admissionregistrationv1.WebhookClientConfig{
			URL:      &url,
			CABundle: caBundle,
		}
	} else {
		// Production: provider runs as a K8s service
		clientConfig = admissionregistrationv1.WebhookClientConfig{
			Service: &admissionregistrationv1.ServiceReference{
				Name:      serviceName,
				Namespace: serviceNamespace,
				Path:      &path,
				Port:      &webhookPort,
			},
			CABundle: caBundle,
		}
	}

	cfg := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: webhookConfigName,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "attestation-webhook",
				"app.kubernetes.io/part-of":   "provider",
				"app.kubernetes.io/component": "admission-webhook",
			},
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name:                    webhookName,
				AdmissionReviewVersions: []string{"v1"},
				FailurePolicy:           &failPolicy,
				SideEffects:             &sideEffects,
				MatchPolicy:             &matchPolicy,
				TimeoutSeconds:          &timeoutSec,
				ClientConfig:            clientConfig,
				// Only intercept pod creation in tenant lease namespaces.
				// Lease namespaces have both akash.network=true AND akash.network/namespace labels.
				// This excludes akash-services (provider/operator pods) which only has akash.network=true.
				NamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						builder.AkashManagedLabelName: builder.ValTrue,
					},
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      "akash.network/namespace",
							Operator: metav1.LabelSelectorOpExists,
						},
					},
				},
				Rules: []admissionregistrationv1.RuleWithOperations{
					{
						Operations: []admissionregistrationv1.OperationType{
							admissionregistrationv1.Create,
						},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{""},
							APIVersions: []string{"v1"},
							Resources:   []string{"pods"},
						},
					},
				},
			},
		},
	}

	client := kc.AdmissionregistrationV1().MutatingWebhookConfigurations()

	existing, err := client.Get(ctx, webhookConfigName, metav1.GetOptions{})
	if kerrors.IsNotFound(err) {
		_, err = client.Create(ctx, cfg, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("create MutatingWebhookConfiguration: %w", err)
		}
		log.Info("created MutatingWebhookConfiguration", "name", webhookConfigName)
		return nil
	}
	if err != nil {
		return fmt.Errorf("get MutatingWebhookConfiguration: %w", err)
	}

	cfg.ResourceVersion = existing.ResourceVersion
	_, err = client.Update(ctx, cfg, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update MutatingWebhookConfiguration: %w", err)
	}
	log.Info("updated MutatingWebhookConfiguration", "name", webhookConfigName)
	return nil
}

// DeregisterWebhookConfiguration removes the MutatingWebhookConfiguration
// from the cluster. Call this on shutdown to avoid dangling webhooks that
// block pod creation when the provider is not running.
func DeregisterWebhookConfiguration(ctx context.Context, kc kubernetes.Interface, log log.Logger) {
	err := kc.AdmissionregistrationV1().MutatingWebhookConfigurations().Delete(
		ctx, webhookConfigName, metav1.DeleteOptions{},
	)
	if err != nil && !kerrors.IsNotFound(err) {
		log.Error("failed to deregister MutatingWebhookConfiguration", "err", err)
		return
	}
	log.Info("deregistered MutatingWebhookConfiguration", "name", webhookConfigName)
}
