package util_test

import (
	"testing"

	"github.com/ovrclk/akash/testutil"
	"github.com/stretchr/testify/require"

	"github.com/ovrclk/provider-services/cluster/util"
)

func TestPassesThroughNames(t *testing.T) {
	leaseID := testutil.LeaseID(t)

	sharingKey := util.MakeIPSharingKey(leaseID, "foobar")
	require.Contains(t, sharingKey, "foobar")
}

func TestFiltersUnderscore(t *testing.T) {
	leaseID := testutil.LeaseID(t)

	sharingKey := util.MakeIPSharingKey(leaseID, "me_you")
	require.NotContains(t, sharingKey, "me_you")
}

func TestFiltersUppercase(t *testing.T) {
	leaseID := testutil.LeaseID(t)

	sharingKey := util.MakeIPSharingKey(leaseID, "meYOU")
	require.NotContains(t, sharingKey, "meYOU")

	require.Equal(t, sharingKey, leaseID.GetOwner()+"-ip-ps9pn7rkocct7m9ivtovuktb")
}
