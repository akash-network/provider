package bidengine

import (
	"testing"

	"github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	atypes "github.com/akash-network/akash-api/go/node/types/v1beta3"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
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
		req  Request
		res  ctypes.Reservation
		gpu  gpuElement
	}{
		{
			desc: "wildcard and reservation value",
			req: Request{
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
			res: &testReservation{
				ru: dtypes.ResourceUnits{
					{
						Resources: atypes.Resources{
							ID: 111,
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
			req: Request{
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
			res: &testReservation{
				ru: dtypes.ResourceUnits{
					{
						Resources: atypes.Resources{
							ID: 111,
							GPU: &atypes.GPU{
								Units: atypes.ResourceValue{
									Val: sdk.NewInt(111),
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
			req: Request{
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
			},
			res: &testReservation{
				ru: dtypes.ResourceUnits{
					{
						Resources: atypes.Resources{
							ID: 111,
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
			d := newDataForScript(c.req, c.res)
			assert.NotEmpty(t, d)
		})
	}
}
