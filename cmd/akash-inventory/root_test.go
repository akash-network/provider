package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/akash-network/provider/verification/inventory/hostprobe"
)

func TestRootCommandWritesSnapshot(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "/proc/cpuinfo", "processor: 0\n")
	writeFile(t, root, "/sys/devices/system/cpu/online", "0")
	writeFile(t, root, "/proc/sys/kernel/osrelease", "6.1.0-akash")

	var out bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{
		"--root", root,
		"--source-timeout", "1s",
	})

	require.NoError(t, cmd.Execute())

	var snapshot hostprobe.Snapshot
	require.NoError(t, json.Unmarshal(out.Bytes(), &snapshot))
	require.Equal(t, hostprobe.SnapshotSchemaVersion, snapshot.SchemaVersion)
	require.Equal(t, "6.1.0-akash", snapshot.Host.KernelRelease)
}

func TestRootCommandWritesOutputFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "/proc/cpuinfo", "processor: 0\n")

	output := filepath.Join(t.TempDir(), "snapshot.json")
	cmd := newRootCmd()
	cmd.SetArgs([]string{
		"--root", root,
		"--output", output,
		"--pretty",
	})

	require.NoError(t, cmd.Execute())

	data, err := os.ReadFile(output)
	require.NoError(t, err)
	require.Contains(t, string(data), "\n  \"schema_version\"")
}

func writeFile(t *testing.T, root string, path string, data string) {
	t.Helper()

	fullPath := filepath.Join(root, path)
	require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
	require.NoError(t, os.WriteFile(fullPath, []byte(data), 0o644))
}
