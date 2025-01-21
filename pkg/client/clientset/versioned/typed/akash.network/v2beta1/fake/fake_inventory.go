/*
Copyright The Akash Network Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	v2beta1 "github.com/akash-network/provider/pkg/apis/akash.network/v2beta1"
	akashnetworkv2beta1 "github.com/akash-network/provider/pkg/client/applyconfiguration/akash.network/v2beta1"
	typedakashnetworkv2beta1 "github.com/akash-network/provider/pkg/client/clientset/versioned/typed/akash.network/v2beta1"
	gentype "k8s.io/client-go/gentype"
)

// fakeInventories implements InventoryInterface
type fakeInventories struct {
	*gentype.FakeClientWithListAndApply[*v2beta1.Inventory, *v2beta1.InventoryList, *akashnetworkv2beta1.InventoryApplyConfiguration]
	Fake *FakeAkashV2beta1
}

func newFakeInventories(fake *FakeAkashV2beta1) typedakashnetworkv2beta1.InventoryInterface {
	return &fakeInventories{
		gentype.NewFakeClientWithListAndApply[*v2beta1.Inventory, *v2beta1.InventoryList, *akashnetworkv2beta1.InventoryApplyConfiguration](
			fake.Fake,
			"",
			v2beta1.SchemeGroupVersion.WithResource("inventories"),
			v2beta1.SchemeGroupVersion.WithKind("Inventory"),
			func() *v2beta1.Inventory { return &v2beta1.Inventory{} },
			func() *v2beta1.InventoryList { return &v2beta1.InventoryList{} },
			func(dst, src *v2beta1.InventoryList) { dst.ListMeta = src.ListMeta },
			func(list *v2beta1.InventoryList) []*v2beta1.Inventory { return gentype.ToPointerSlice(list.Items) },
			func(list *v2beta1.InventoryList, items []*v2beta1.Inventory) {
				list.Items = gentype.FromPointerSlice(items)
			},
		),
		fake,
	}
}
