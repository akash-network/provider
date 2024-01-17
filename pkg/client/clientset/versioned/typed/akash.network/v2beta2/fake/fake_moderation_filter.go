package fake

import (
	"context"

	v2beta2 "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	schema "k8s.io/apimachinery/pkg/runtime/schema"
	testing "k8s.io/client-go/testing"
)

// FakeModerationFilters implements ModerationFilterInterface
type FakeModerationFilters struct {
	Fake *FakeAkashV2beta2
	ns   string
}

var moderationfiltersResource = schema.GroupVersionResource{Group: "akash.network", Version: "v2beta2", Resource: "moderationfilters"}

var moderationfiltersKind = schema.GroupVersionKind{Group: "akash.network", Version: "v2beta2", Kind: "ModerationFilter"}

// Get takes name of the moderationfilter, and returns the corresponding moderationfilter object, and an error if there is any.
func (c *FakeModerationFilters) Get(ctx context.Context, name string, options v1.GetOptions) (result *v2beta2.ModerationFilter, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewGetAction(moderationfiltersResource, c.ns, name), &v2beta2.ModerationFilter{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v2beta2.ModerationFilter), err
}

// List takes label and field selectors, and returns the list of ModerationFilters that match those selectors.
func (c *FakeModerationFilters) List(ctx context.Context, opts v1.ListOptions) (result *v2beta2.ModerationFilterList, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewListAction(moderationfiltersResource, moderationfiltersKind, c.ns, opts), &v2beta2.ModerationFilterList{})

	if obj == nil {
		return nil, err
	}

	label, _, _ := testing.ExtractFromListOptions(opts)
	if label == nil {
		label = labels.Everything()
	}
	list := &v2beta2.ModerationFilterList{ListMeta: obj.(*v2beta2.ModerationFilterList).ListMeta}
	for _, item := range obj.(*v2beta2.ModerationFilterList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}
