package kube

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeriveDEK(t *testing.T) {
	master := []byte("test-master-key-32-bytes-long!!!")

	a := deriveDEK(master, "kbs:///nsA/data/dek")
	b := deriveDEK(master, "kbs:///nsA/data/dek")
	c := deriveDEK(master, "kbs:///nsB/data/dek")

	// Deterministic: same inputs -> same DEK (so data stays decryptable).
	require.Equal(t, a, b)
	// Distinct per volume/lease (URI): tenants never share a key.
	require.NotEqual(t, a, c)
	// 32-byte HMAC -> 64 lowercase hex chars; no NUL/newline (cryptsetup-safe).
	require.Len(t, a, 64)
	require.NotContains(t, string(a), "\n")

	// Different master key -> different DEK.
	require.NotEqual(t, a, deriveDEK([]byte("another-master-key-............."), "kbs:///nsA/data/dek"))
}

func TestKBSResourcePath(t *testing.T) {
	p, err := kbsResourcePath("kbs:///nsA/data/dek")
	require.NoError(t, err)
	require.Equal(t, "/kbs/v0/resource/nsA/data/dek", p)

	for _, bad := range []string{"", "kbs:///", "http://x/y", "nsA/data/dek"} {
		_, err := kbsResourcePath(bad)
		require.Error(t, err, "expected error for %q", bad)
	}
}
