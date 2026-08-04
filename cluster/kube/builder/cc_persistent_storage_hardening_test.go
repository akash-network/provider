package builder

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCCPersistentVolumeIDIgnoresSignatureButBindsLeaseAndPayload(t *testing.T) {
	workload := newCCStorageWorkload(t, RuntimeClassKataQemuSNP,
		ccStorageSpec{name: "data", mount: "/proof", persistent: true, keyRef: testSealedKeyRef},
	)
	leaseID := workload.deployment.LeaseID()
	parts := strings.Split(testSealedKeyRef, ".")
	require.Len(t, parts, 4)
	resigned := strings.Join([]string{parts[0], parts[1], parts[2], "YW5vdGhlci1zaWduYXR1cmU"}, ".")

	first, err := ccPersistentVolumeID(leaseID, "proof", "data", testSealedKeyRef)
	require.NoError(t, err)
	second, err := ccPersistentVolumeID(leaseID, "proof", "data", resigned)
	require.NoError(t, err)
	require.Equal(t, first, second, "signature bytes must not change persistent identity")
	require.True(t, strings.HasPrefix(first, "akash:v2:"))

	changedPayload := strings.Join([]string{parts[0], parts[1], "eyJuYW1lIjoiZGlmZmVyZW50In0", parts[3]}, ".")
	otherPayloadID, err := ccPersistentVolumeID(leaseID, "proof", "data", changedPayload)
	require.NoError(t, err)
	require.NotEqual(t, first, otherPayloadID)

	otherLease := leaseID
	otherLease.Owner += "-other"
	otherTenantID, err := ccPersistentVolumeID(otherLease, "proof", "data", testSealedKeyRef)
	require.NoError(t, err)
	require.NotEqual(t, first, otherTenantID)

	otherServiceID, err := ccPersistentVolumeID(leaseID, "other-service", "data", testSealedKeyRef)
	require.NoError(t, err)
	require.NotEqual(t, first, otherServiceID)

	otherVolumeID, err := ccPersistentVolumeID(leaseID, "proof", "other-volume", testSealedKeyRef)
	require.NoError(t, err)
	require.NotEqual(t, first, otherVolumeID)

	for _, malformed := range []string{
		"sealed.header.payload",
		"sealed.header.***.signature",
		"sealed..payload.signature",
	} {
		_, err := ccPersistentVolumeID(leaseID, "proof", "data", malformed)
		require.Error(t, err)
	}
}

func TestConfidentialPersistentStorageFailsClosedForReplicasAndClasses(t *testing.T) {
	workload := newCCStorageWorkload(t, RuntimeClassKataQemuSNP,
		ccStorageSpec{name: "data", mount: "/proof", persistent: true, keyRef: testSealedKeyRef, class: "beta3"},
	)
	workload.group.Services[0].Count = 2
	_, err := workload.confidentialPersistentVolumes()
	require.ErrorContains(t, err, "exactly one replica")

	workload = newCCStorageWorkload(t, RuntimeClassKataQemuSNP,
		ccStorageSpec{name: "data", mount: "/proof", persistent: true, keyRef: testSealedKeyRef, class: "beta3"},
	)
	workload.settings.CCPersistentStorageClasses = nil
	_, err = workload.confidentialPersistentVolumes()
	require.ErrorContains(t, err, "not allowlisted")
}

func TestConfidentialPersistentStorageClassSettingsValidation(t *testing.T) {
	err := ValidateSettings(Settings{
		CCPersistentStorageClasses: map[string]struct{}{"Not A Storage Class": {}},
	})
	require.ErrorContains(t, err, "invalid confidential persistent storage class")

	err = ValidateSettings(Settings{
		CCPersistentStorageClasses: map[string]struct{}{"beta3": {}},
	})
	require.ErrorContains(t, err, "require confidential-compute initdata")
}
