package kube

// nolint:deadcode,golint

import (
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"

	metricsutils "github.com/akash-network/node/util/metrics"
	"github.com/akash-network/provider/cluster/kube/builder"
	crdapi "github.com/akash-network/provider/pkg/client/clientset/versioned"
	"k8s.io/client-go/util/retry"
)

func applyNS(ctx context.Context, kc kubernetes.Interface, b builder.NS) error {
	obj, err := kc.CoreV1().Namespaces().Get(ctx, b.Name(), metav1.GetOptions{})
	metricsutils.IncCounterVecWithLabelValuesFiltered(kubeCallsCounter, "namespaces-get", err, errors.IsNotFound)

	switch {
	case err == nil:
		obj, err = b.Update(obj)
		if err == nil {
			_, err = kc.CoreV1().Namespaces().Update(ctx, obj, metav1.UpdateOptions{})
			metricsutils.IncCounterVecWithLabelValues(kubeCallsCounter, "namespaces-update", err)
		}
	case errors.IsNotFound(err):
		obj, err = b.Create()
		if err == nil {
			_, err = kc.CoreV1().Namespaces().Create(ctx, obj, metav1.CreateOptions{})
			metricsutils.IncCounterVecWithLabelValues(kubeCallsCounter, "namespaces-create", err)
		}
	}
	return err
}

// Apply list of Network Policies
func applyNetPolicies(ctx context.Context, kc kubernetes.Interface, b builder.NetPol) error {
	var err error

	policies, err := b.Create()
	if err != nil {
		return err
	}

	for _, pol := range policies {
		obj, err := kc.NetworkingV1().NetworkPolicies(b.NS()).Get(ctx, pol.Name, metav1.GetOptions{})
		metricsutils.IncCounterVecWithLabelValuesFiltered(kubeCallsCounter, "networking-policies-get", err, errors.IsNotFound)

		switch {
		case err == nil:
			_, err = b.Update(obj)
			if err == nil {
				_, err = kc.NetworkingV1().NetworkPolicies(b.NS()).Update(ctx, pol, metav1.UpdateOptions{})
				metricsutils.IncCounterVecWithLabelValues(kubeCallsCounter, "networking-policies-update", err)
			}
		case errors.IsNotFound(err):
			_, err = kc.NetworkingV1().NetworkPolicies(b.NS()).Create(ctx, pol, metav1.CreateOptions{})
			metricsutils.IncCounterVecWithLabelValues(kubeCallsCounter, "networking-policies-create", err)
		}
		if err != nil {
			break
		}
	}

	return err
}

// TODO: re-enable.  see #946
// func applyRestrictivePodSecPoliciesToNS(ctx context.Context, kc kubernetes.Interface, p builder.PspRestricted) error {
// 	obj, err := kc.PolicyV1beta1().PodSecurityPolicies().Get(ctx, p.Name(), metav1.GetOptions{})
// 	switch {
// 	case err == nil:
// 		obj, err = p.Update(obj)
// 		if err == nil {
// 			_, err = kc.PolicyV1beta1().PodSecurityPolicies().Update(ctx, obj, metav1.UpdateOptions{})
// 		}
// 	case errors.IsNotFound(err):
// 		obj, err = p.Create()
// 		if err == nil {
// 			_, err = kc.PolicyV1beta1().PodSecurityPolicies().Create(ctx, obj, metav1.CreateOptions{})
// 		}
// 	}
// 	return err
// }

func applyServiceCredentials(ctx context.Context, kc kubernetes.Interface, b builder.ServiceCredentials) error {
	obj, err := kc.CoreV1().Secrets(b.NS()).Get(ctx, b.Name(), metav1.GetOptions{})
	metricsutils.IncCounterVecWithLabelValuesFiltered(kubeCallsCounter, "secrets-get", err, errors.IsNotFound)

	switch {
	case err == nil:
		obj, err = b.Update(obj)
		if err == nil {
			_, err = kc.CoreV1().Secrets(b.NS()).Update(ctx, obj, metav1.UpdateOptions{})
			metricsutils.IncCounterVecWithLabelValues(kubeCallsCounter, "secrets-get", err)

		}
	case errors.IsNotFound(err):
		obj, err = b.Create()
		if err == nil {
			_, err = kc.CoreV1().Secrets(b.NS()).Create(ctx, obj, metav1.CreateOptions{})
			metricsutils.IncCounterVecWithLabelValues(kubeCallsCounter, "secrets-create", err)
		}
	}
	return err

}

func applyDeployment(ctx context.Context, kc kubernetes.Interface, b builder.Deployment) error {
	obj, err := kc.AppsV1().Deployments(b.NS()).Get(ctx, b.Name(), metav1.GetOptions{})
	metricsutils.IncCounterVecWithLabelValuesFiltered(kubeCallsCounter, "deployments-get", err, errors.IsNotFound)

	switch {
	case err == nil:
		retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			obj, err = kc.AppsV1().Deployments(b.NS()).Get(ctx, b.Name(), metav1.GetOptions{})
			if err != nil {
				return err
			}

			obj, err = b.Update(obj)
			if err != nil {
				return err
			}

			result, err := kc.AppsV1().Deployments(b.NS()).Update(ctx, obj, metav1.UpdateOptions{})
			if err != nil {
				return err
			}

			getFunc := func(ctx context.Context, name, ns string, opts metav1.GetOptions) (interface{}, error) {
				return kc.AppsV1().Deployments(ns).Get(ctx, name, opts)
			}
			watchFunc := func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
				return kc.AppsV1().Deployments(b.NS()).Watch(ctx, opts)
			}
			return waitForProcessedVersion(ctx, getFunc, watchFunc, b.NS(), b.Name(), result.ResourceVersion)
		})
		if retryErr != nil {
			return retryErr
		}

	case errors.IsNotFound(err):
		obj, err = b.Create()
		if err == nil {
			result, err := kc.AppsV1().Deployments(b.NS()).Create(ctx, obj, metav1.CreateOptions{})
			metricsutils.IncCounterVecWithLabelValues(kubeCallsCounter, "deployments-create", err)
			if err != nil {
				return err
			}

			getFunc := func(ctx context.Context, name, ns string, opts metav1.GetOptions) (interface{}, error) {
				return kc.AppsV1().Deployments(ns).Get(ctx, name, opts)
			}
			watchFunc := func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
				return kc.AppsV1().Deployments(b.NS()).Watch(ctx, opts)
			}
			return waitForProcessedVersion(ctx, getFunc, watchFunc, b.NS(), b.Name(), result.ResourceVersion)
		}
	}
	return err
}

func applyStatefulSet(ctx context.Context, kc kubernetes.Interface, b builder.StatefulSet) error {
	obj, err := kc.AppsV1().StatefulSets(b.NS()).Get(ctx, b.Name(), metav1.GetOptions{})
	metricsutils.IncCounterVecWithLabelValuesFiltered(kubeCallsCounter, "statefulset-get", err, errors.IsNotFound)

	switch {
	case err == nil:
		retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			obj, err = kc.AppsV1().StatefulSets(b.NS()).Get(ctx, b.Name(), metav1.GetOptions{})
			if err != nil {
				return err
			}

			obj, err = b.Update(obj)
			if err != nil {
				return err
			}

			result, err := kc.AppsV1().StatefulSets(b.NS()).Update(ctx, obj, metav1.UpdateOptions{})
			if err != nil {
				return err
			}

			getFunc := func(ctx context.Context, name, ns string, opts metav1.GetOptions) (interface{}, error) {
				return kc.AppsV1().StatefulSets(ns).Get(ctx, name, opts)
			}
			watchFunc := func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
				return kc.AppsV1().StatefulSets(b.NS()).Watch(ctx, opts)
			}
			return waitForProcessedVersion(ctx, getFunc, watchFunc, b.NS(), b.Name(), result.ResourceVersion)
		})
		if retryErr != nil {
			return retryErr
		}

	case errors.IsNotFound(err):
		obj, err = b.Create()
		if err == nil {
			result, err := kc.AppsV1().StatefulSets(b.NS()).Create(ctx, obj, metav1.CreateOptions{})
			metricsutils.IncCounterVecWithLabelValues(kubeCallsCounter, "statefulset-create", err)
			if err != nil {
				return err
			}

			getFunc := func(ctx context.Context, name, ns string, opts metav1.GetOptions) (interface{}, error) {
				return kc.AppsV1().StatefulSets(ns).Get(ctx, name, opts)
			}
			watchFunc := func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
				return kc.AppsV1().StatefulSets(b.NS()).Watch(ctx, opts)
			}
			return waitForProcessedVersion(ctx, getFunc, watchFunc, b.NS(), b.Name(), result.ResourceVersion)
		}
	}
	return err
}

func applyService(ctx context.Context, kc kubernetes.Interface, b builder.Service) error {
	obj, err := kc.CoreV1().Services(b.NS()).Get(ctx, b.Name(), metav1.GetOptions{})
	metricsutils.IncCounterVecWithLabelValuesFiltered(kubeCallsCounter, "services-get", err, errors.IsNotFound)

	switch {
	case err == nil:
		obj, err = b.Update(obj)
		if err == nil {
			_, err = kc.CoreV1().Services(b.NS()).Update(ctx, obj, metav1.UpdateOptions{})
			metricsutils.IncCounterVecWithLabelValues(kubeCallsCounter, "services-update", err)
		}
	case errors.IsNotFound(err):
		obj, err = b.Create()
		if err == nil {
			_, err = kc.CoreV1().Services(b.NS()).Create(ctx, obj, metav1.CreateOptions{})
			metricsutils.IncCounterVecWithLabelValues(kubeCallsCounter, "services-create", err)
		}
	}
	return err
}

func applyManifest(ctx context.Context, kc crdapi.Interface, b builder.Manifest) error {
	obj, err := kc.AkashV2beta2().Manifests(b.NS()).Get(ctx, b.Name(), metav1.GetOptions{})

	metricsutils.IncCounterVecWithLabelValuesFiltered(kubeCallsCounter, "akash-manifests-get", err, errors.IsNotFound)

	switch {
	case err == nil:
		// TODO - only run this update if it would change something
		obj, err = b.Update(obj)
		if err == nil {
			_, err = kc.AkashV2beta2().Manifests(b.NS()).Update(ctx, obj, metav1.UpdateOptions{})
			metricsutils.IncCounterVecWithLabelValues(kubeCallsCounter, "akash-manifests-update", err)
		}
	case errors.IsNotFound(err):
		obj, err = b.Create()
		if err == nil {
			_, err = kc.AkashV2beta2().Manifests(b.NS()).Create(ctx, obj, metav1.CreateOptions{})
			metricsutils.IncCounterVecWithLabelValues(kubeCallsCounter, "akash-manifests-create", err)
		}
	}

	return err
}

func waitForProcessedVersion(ctx context.Context,
	getFunc func(ctx context.Context, name, ns string, opts metav1.GetOptions) (interface{}, error),
	watchFunc func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error),
	ns, name string, resourceVersion string) error {

	// First try to get the object with the new resource version
	_, err := getFunc(ctx, name, ns, metav1.GetOptions{
		ResourceVersion: resourceVersion,
	})
	if err == nil {
		return nil
	}
	if !errors.IsNotFound(err) && !errors.IsConflict(err) {
		return err
	}

	// If we couldn't get it, then watch for changes
	watcher, err := watchFunc(ctx, metav1.ListOptions{
		FieldSelector:   fmt.Sprintf("metadata.name=%s", name),
		ResourceVersion: resourceVersion,
	})
	if err != nil {
		return err
	}
	defer watcher.Stop()

	for {
		select {
		case event := <-watcher.ResultChan():
			if event.Type == watch.Modified {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
