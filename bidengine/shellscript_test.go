package bidengine

import (
	"testing"

	"github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	atypes "github.com/akash-network/akash-api/go/node/types/v1beta3"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_parseGPU_LastAttribute(t *testing.T) {
	gpu := atypes.GPU{
		Units: atypes.ResourceValue{
			Val: sdk.NewInt(111),
		},
		Attributes: atypes.Attributes{
			{
				Key:   "vendor/nvidia/model/a100",
				Value: "true",
			},
			{
				Key:   "vendor/nvidia/model/v100",
				Value: "true",
			},
			{
				Key:   "vendor/nvidia/model/h100",
				Value: "true",
			},
		},
	}

	e := parseGPU(&gpu)

	v, ok := e.Attributes.Vendor["nvidia"]
	require.True(t, ok)

	assert.Equal(t, "h100", v.Model)
}

func Test_newDataForScript_GPUWildcard(t *testing.T) {
	cases := []struct {
		desc string
		r    Request
		gpu  gpuElement
	}{
		{
			desc: "wildcard and allocated resources",
			r: Request{
				GSpec: &v1beta3.GroupSpec{
					Resources: v1beta3.ResourceUnits{
						{
							Price: sdk.NewDecCoin("denom", sdk.NewInt(111)),
							Resources: atypes.Resources{
								CPU: &atypes.CPU{
									Units: atypes.ResourceValue{
										Val: sdk.NewInt(111),
									},
								},
								Memory: &atypes.Memory{
									Quantity: atypes.ResourceValue{
										Val: sdk.NewInt(111),
									},
								},
								GPU: &atypes.GPU{
									Units: atypes.ResourceValue{
										Val: sdk.NewInt(111),
									},
									Attributes: atypes.Attributes{
										{
											Key:   "vendor/nvidia/model/*",
											Value: "true",
										},
									},
								},
							},
						},
					},
				},
				AllocatedResources: dtypes.ResourceUnits{
					{
						Resources: atypes.Resources{
							ID: 111,
							CPU: &atypes.CPU{
								Units: atypes.ResourceValue{
									Val: sdk.NewInt(111),
								},
							},
							Memory: &atypes.Memory{
								Quantity: atypes.ResourceValue{
									Val: sdk.NewInt(111),
								},
							},
							GPU: &atypes.GPU{
								Units: atypes.ResourceValue{
									Val: sdk.NewInt(111),
								},
								Attributes: atypes.Attributes{
									{
										Key:   "vendor/nvidia/model/a100",
										Value: "true",
									},
								},
							},
						},
					},
				},
			},
			gpu: gpuElement{
				Units: 111,
				Attributes: gpuAttributes{
					Vendor: map[string]gpuVendorAttributes{
						"nvidia": {Model: "a100"},
					},
				},
			},
		},
		{
			desc: "wildcard and no reservation value",
			r: Request{
				GSpec: &v1beta3.GroupSpec{
					Resources: v1beta3.ResourceUnits{
						{
							Price: sdk.NewDecCoin("denom", sdk.NewInt(111)),
							Resources: atypes.Resources{
								CPU: &atypes.CPU{
									Units: atypes.ResourceValue{
										Val: sdk.NewInt(111),
									},
								},
								Memory: &atypes.Memory{
									Quantity: atypes.ResourceValue{
										Val: sdk.NewInt(111),
									},
								},
								GPU: &atypes.GPU{
									Units: atypes.ResourceValue{
										Val: sdk.NewInt(111),
									},
									Attributes: atypes.Attributes{
										{
											Key:   "vendor/nvidia/model/*",
											Value: "true",
										},
									},
								},
							},
						},
					},
				},
			},
			gpu: gpuElement{
				Units: 111,
				Attributes: gpuAttributes{
					Vendor: map[string]gpuVendorAttributes{
						"nvidia": {Model: "*"},
					},
				},
			},
		},
		{
			desc: "no wildcard and reservation value",
			r: Request{
				GSpec: &v1beta3.GroupSpec{
					Resources: v1beta3.ResourceUnits{
						{
							Price: sdk.NewDecCoin("denom", sdk.NewInt(111)),
							Resources: atypes.Resources{
								CPU: &atypes.CPU{
									Units: atypes.ResourceValue{
										Val: sdk.NewInt(111),
									},
								},
								Memory: &atypes.Memory{
									Quantity: atypes.ResourceValue{
										Val: sdk.NewInt(111),
									},
								},
								GPU: &atypes.GPU{
									Units: atypes.ResourceValue{
										Val: sdk.NewInt(111),
									},
									Attributes: atypes.Attributes{
										{
											Key:   "vendor/nvidia/model/h100",
											Value: "true",
										},
									},
								},
							},
						},
					},
				},
				AllocatedResources: dtypes.ResourceUnits{
					{
						Resources: atypes.Resources{
							ID: 111,
							CPU: &atypes.CPU{
								Units: atypes.ResourceValue{
									Val: sdk.NewInt(111),
								},
							},
							Memory: &atypes.Memory{
								Quantity: atypes.ResourceValue{
									Val: sdk.NewInt(111),
								},
							},
							GPU: &atypes.GPU{
								Units: atypes.ResourceValue{
									Val: sdk.NewInt(111),
								},
								Attributes: atypes.Attributes{
									{
										Key:   "vendor/nvidia/model/a100",
										Value: "true",
									},
								},
							},
						},
					},
				},
			},
			gpu: gpuElement{
				Units: 111,
				Attributes: gpuAttributes{
					Vendor: map[string]gpuVendorAttributes{
						"nvidia": {Model: "h100"},
					},
				},
			},
		},
	}

	for _, c := range cases {
		c := c

		t.Run(c.desc, func(t *testing.T) {
			d := newDataForScript(c.r)
			assert.NotEmpty(t, d)
		})
	}
}
