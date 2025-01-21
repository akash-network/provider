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

// Code generated by applyconfiguration-gen. DO NOT EDIT.

package applyconfiguration

import (
	v2beta1 "github.com/akash-network/provider/pkg/apis/akash.network/v2beta1"
	v2beta2 "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	akashnetworkv2beta1 "github.com/akash-network/provider/pkg/client/applyconfiguration/akash.network/v2beta1"
	akashnetworkv2beta2 "github.com/akash-network/provider/pkg/client/applyconfiguration/akash.network/v2beta2"
	internal "github.com/akash-network/provider/pkg/client/applyconfiguration/internal"
	runtime "k8s.io/apimachinery/pkg/runtime"
	schema "k8s.io/apimachinery/pkg/runtime/schema"
	testing "k8s.io/client-go/testing"
)

// ForKind returns an apply configuration type for the given GroupVersionKind, or nil if no
// apply configuration type exists for the given GroupVersionKind.
func ForKind(kind schema.GroupVersionKind) interface{} {
	switch kind {
	// Group=akash.network, Version=v2beta1
	case v2beta1.SchemeGroupVersion.WithKind("Inventory"):
		return &akashnetworkv2beta1.InventoryApplyConfiguration{}
	case v2beta1.SchemeGroupVersion.WithKind("InventoryClusterStorage"):
		return &akashnetworkv2beta1.InventoryClusterStorageApplyConfiguration{}
	case v2beta1.SchemeGroupVersion.WithKind("InventoryRequest"):
		return &akashnetworkv2beta1.InventoryRequestApplyConfiguration{}
	case v2beta1.SchemeGroupVersion.WithKind("InventoryRequestSpec"):
		return &akashnetworkv2beta1.InventoryRequestSpecApplyConfiguration{}
	case v2beta1.SchemeGroupVersion.WithKind("InventoryRequestStatus"):
		return &akashnetworkv2beta1.InventoryRequestStatusApplyConfiguration{}
	case v2beta1.SchemeGroupVersion.WithKind("InventorySpec"):
		return &akashnetworkv2beta1.InventorySpecApplyConfiguration{}
	case v2beta1.SchemeGroupVersion.WithKind("InventoryStatus"):
		return &akashnetworkv2beta1.InventoryStatusApplyConfiguration{}
	case v2beta1.SchemeGroupVersion.WithKind("LeaseID"):
		return &akashnetworkv2beta1.LeaseIDApplyConfiguration{}
	case v2beta1.SchemeGroupVersion.WithKind("Manifest"):
		return &akashnetworkv2beta1.ManifestApplyConfiguration{}
	case v2beta1.SchemeGroupVersion.WithKind("ManifestGroup"):
		return &akashnetworkv2beta1.ManifestGroupApplyConfiguration{}
	case v2beta1.SchemeGroupVersion.WithKind("ManifestService"):
		return &akashnetworkv2beta1.ManifestServiceApplyConfiguration{}
	case v2beta1.SchemeGroupVersion.WithKind("ManifestServiceExpose"):
		return &akashnetworkv2beta1.ManifestServiceExposeApplyConfiguration{}
	case v2beta1.SchemeGroupVersion.WithKind("ManifestServiceExposeHTTPOptions"):
		return &akashnetworkv2beta1.ManifestServiceExposeHTTPOptionsApplyConfiguration{}
	case v2beta1.SchemeGroupVersion.WithKind("ManifestServiceParams"):
		return &akashnetworkv2beta1.ManifestServiceParamsApplyConfiguration{}
	case v2beta1.SchemeGroupVersion.WithKind("ManifestServiceStorage"):
		return &akashnetworkv2beta1.ManifestServiceStorageApplyConfiguration{}
	case v2beta1.SchemeGroupVersion.WithKind("ManifestSpec"):
		return &akashnetworkv2beta1.ManifestSpecApplyConfiguration{}
	case v2beta1.SchemeGroupVersion.WithKind("ManifestStatus"):
		return &akashnetworkv2beta1.ManifestStatusApplyConfiguration{}
	case v2beta1.SchemeGroupVersion.WithKind("ManifestStorageParams"):
		return &akashnetworkv2beta1.ManifestStorageParamsApplyConfiguration{}
	case v2beta1.SchemeGroupVersion.WithKind("ProviderHost"):
		return &akashnetworkv2beta1.ProviderHostApplyConfiguration{}
	case v2beta1.SchemeGroupVersion.WithKind("ProviderHostSpec"):
		return &akashnetworkv2beta1.ProviderHostSpecApplyConfiguration{}
	case v2beta1.SchemeGroupVersion.WithKind("ProviderHostStatus"):
		return &akashnetworkv2beta1.ProviderHostStatusApplyConfiguration{}
	case v2beta1.SchemeGroupVersion.WithKind("ProviderLeasedIP"):
		return &akashnetworkv2beta1.ProviderLeasedIPApplyConfiguration{}
	case v2beta1.SchemeGroupVersion.WithKind("ProviderLeasedIPSpec"):
		return &akashnetworkv2beta1.ProviderLeasedIPSpecApplyConfiguration{}
	case v2beta1.SchemeGroupVersion.WithKind("ProviderLeasedIPStatus"):
		return &akashnetworkv2beta1.ProviderLeasedIPStatusApplyConfiguration{}
	case v2beta1.SchemeGroupVersion.WithKind("ResourcePair"):
		return &akashnetworkv2beta1.ResourcePairApplyConfiguration{}
	case v2beta1.SchemeGroupVersion.WithKind("ResourceUnits"):
		return &akashnetworkv2beta1.ResourceUnitsApplyConfiguration{}

		// Group=akash.network, Version=v2beta2
	case v2beta2.SchemeGroupVersion.WithKind("Inventory"):
		return &akashnetworkv2beta2.InventoryApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("InventoryClusterStorage"):
		return &akashnetworkv2beta2.InventoryClusterStorageApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("InventoryRequest"):
		return &akashnetworkv2beta2.InventoryRequestApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("InventoryRequestSpec"):
		return &akashnetworkv2beta2.InventoryRequestSpecApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("InventoryRequestStatus"):
		return &akashnetworkv2beta2.InventoryRequestStatusApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("InventorySpec"):
		return &akashnetworkv2beta2.InventorySpecApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("InventoryStatus"):
		return &akashnetworkv2beta2.InventoryStatusApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("LeaseID"):
		return &akashnetworkv2beta2.LeaseIDApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("Manifest"):
		return &akashnetworkv2beta2.ManifestApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("ManifestGroup"):
		return &akashnetworkv2beta2.ManifestGroupApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("ManifestService"):
		return &akashnetworkv2beta2.ManifestServiceApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("ManifestServiceCredentials"):
		return &akashnetworkv2beta2.ManifestServiceCredentialsApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("ManifestServiceExpose"):
		return &akashnetworkv2beta2.ManifestServiceExposeApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("ManifestServiceExposeHTTPOptions"):
		return &akashnetworkv2beta2.ManifestServiceExposeHTTPOptionsApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("ManifestServiceParams"):
		return &akashnetworkv2beta2.ManifestServiceParamsApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("ManifestSpec"):
		return &akashnetworkv2beta2.ManifestSpecApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("ManifestStorageParams"):
		return &akashnetworkv2beta2.ManifestStorageParamsApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("ProviderHost"):
		return &akashnetworkv2beta2.ProviderHostApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("ProviderHostSpec"):
		return &akashnetworkv2beta2.ProviderHostSpecApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("ProviderLeasedIP"):
		return &akashnetworkv2beta2.ProviderLeasedIPApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("ProviderLeasedIPSpec"):
		return &akashnetworkv2beta2.ProviderLeasedIPSpecApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("ResourceCPU"):
		return &akashnetworkv2beta2.ResourceCPUApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("ResourceGPU"):
		return &akashnetworkv2beta2.ResourceGPUApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("ResourceMemory"):
		return &akashnetworkv2beta2.ResourceMemoryApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("ResourcePair"):
		return &akashnetworkv2beta2.ResourcePairApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("Resources"):
		return &akashnetworkv2beta2.ResourcesApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("ResourceVolume"):
		return &akashnetworkv2beta2.ResourceVolumeApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("SchedulerParams"):
		return &akashnetworkv2beta2.SchedulerParamsApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("SchedulerResourceGPU"):
		return &akashnetworkv2beta2.SchedulerResourceGPUApplyConfiguration{}
	case v2beta2.SchemeGroupVersion.WithKind("SchedulerResources"):
		return &akashnetworkv2beta2.SchedulerResourcesApplyConfiguration{}

	}
	return nil
}

func NewTypeConverter(scheme *runtime.Scheme) *testing.TypeConverter {
	return &testing.TypeConverter{Scheme: scheme, TypeResolver: internal.Parser()}
}
