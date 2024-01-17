package kube

import (
	"context"
	"fmt"

	"github.com/akash-network/provider/cluster/kube/builder"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/pager"

	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

func (c *client) AllModerationFilters(ctx context.Context) ([]ctypes.ActiveFilters, error) {
	result := make([]ctypes.ActiveFilters, 0)

	filterPager := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
		return c.ac.AkashV2beta2().ModerationFilters(c.ns).List(ctx, opts)
	})

	listOptions := metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=true", builder.AkashManagedLabelName),
	}

	err := filterPager.EachListItem(ctx, listOptions, func(obj runtime.Object) error {
		fp := obj.(*crd.ModerationFilter)

		for _, filter := range fp.Spec {
			result = append(result, ctypes.ActiveFilters{
				Allow: filter.Allow,
				Type: filter.Type,
				Pattern: filter.Pattern,
			})
		}
		return nil

	})

	if err != nil {
		return nil, err
	}
	return result, nil
}
