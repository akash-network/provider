package certissuer

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/akash-network/node/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCertificatesStorage_MoveToArchive(t *testing.T) {
	domain := "example.com"

	cfg := StorageConfig{
		RootPath: filepath.Join(t.TempDir(), ".certissuer"),
	}

	defer func() {
		err := os.RemoveAll(cfg.RootPath)
		require.NoError(t, err)
	}()

	log := testutil.Logger(t)

	storage, err := NewStorage(log, cfg)
	require.NoError(t, err)

	certsDir := filepath.Join(cfg.RootPath, baseCertificatesRootFolderName)
	archiveDir := filepath.Join(cfg.RootPath, baseArchivesRootFolderName)

	domainFiles := generateTestFiles(t, certsDir, domain)

	err = storage.ArchiveDomain(domain)
	require.NoError(t, err)

	for _, file := range domainFiles {
		assert.NoFileExists(t, file)
	}

	root, err := os.ReadDir(certsDir)
	require.NoError(t, err)
	require.Empty(t, root)

	archive, err := os.ReadDir(archiveDir)
	require.NoError(t, err)

	require.Len(t, archive, len(domainFiles))
	assert.Regexp(t, `\d+\.`+regexp.QuoteMeta(domain), archive[0].Name())
}

func TestCertificatesStorage_MoveToArchive_noFileRelatedToDomain(t *testing.T) {
	domain := "example.com"

	cfg := StorageConfig{
		RootPath: filepath.Join(t.TempDir(), ".certissuer"),
	}

	defer func() {
		err := os.RemoveAll(cfg.RootPath)
		require.NoError(t, err)
	}()

	log := testutil.Logger(t)
	storage, err := NewStorage(log, cfg)
	require.NoError(t, err)

	certsDir := filepath.Join(cfg.RootPath, baseCertificatesRootFolderName)
	archiveDir := filepath.Join(cfg.RootPath, baseArchivesRootFolderName)

	domainFiles := generateTestFiles(t, certsDir, "example.org")

	err = storage.ArchiveDomain(domain)
	require.NoError(t, err)

	for _, file := range domainFiles {
		assert.FileExists(t, file)
	}

	root, err := os.ReadDir(certsDir)
	require.NoError(t, err)
	assert.Len(t, root, len(domainFiles))

	archive, err := os.ReadDir(archiveDir)
	require.NoError(t, err)

	assert.Empty(t, archive)
}

func TestCertificatesStorage_MoveToArchive_ambiguousDomain(t *testing.T) {
	domain := "example.com"

	cfg := StorageConfig{
		RootPath: filepath.Join(t.TempDir(), ".certissuer"),
	}

	defer func() {
		err := os.RemoveAll(cfg.RootPath)
		require.NoError(t, err)
	}()

	log := testutil.Logger(t)
	storage, err := NewStorage(log, cfg)
	require.NoError(t, err)

	certsDir := filepath.Join(cfg.RootPath, baseCertificatesRootFolderName)
	archiveDir := filepath.Join(cfg.RootPath, baseArchivesRootFolderName)

	domainFiles := generateTestFiles(t, certsDir, domain)
	otherDomainFiles := generateTestFiles(t, certsDir, domain+".example.org")

	err = storage.ArchiveDomain(domain)
	require.NoError(t, err)

	for _, file := range domainFiles {
		assert.NoFileExists(t, file)
	}

	for _, file := range otherDomainFiles {
		assert.FileExists(t, file)
	}

	root, err := os.ReadDir(certsDir)
	require.NoError(t, err)
	require.Len(t, root, len(otherDomainFiles))

	archive, err := os.ReadDir(archiveDir)
	require.NoError(t, err)

	require.Len(t, archive, len(domainFiles))
	assert.Regexp(t, `\d+\.`+regexp.QuoteMeta(domain), archive[0].Name())
}

func generateTestFiles(t *testing.T, dir, domain string) []string {
	t.Helper()

	var filenames []string

	for _, ext := range []string{extIssuer, extCert, extKey, extPem, extPfx, extResource} {
		filename := filepath.Join(dir, domain+ext)
		err := os.WriteFile(filename, []byte("test"), filePerm)
		require.NoError(t, err)

		filenames = append(filenames, filename)
	}

	return filenames
}
