package certissuer

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"pkg.akt.dev/go/testutil"
)

const (
	exampleDomain = "example.org"
)

func TestCertificatesStorage_MoveToArchive(t *testing.T) {
	domain := exampleDomain

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

	domainFiles := generateTestFiles(t, certsDir, exampleDomain)

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
	otherDomainFiles := generateTestFiles(t, certsDir, fmt.Sprintf("%s.%s", domain, exampleDomain))

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

	var ext = []string{extIssuer, extCert, extKey, extPem, extPfx, extResource}

	filenames := make([]string, 0, len(ext))

	for _, ext := range ext {
		filename := filepath.Join(dir, domain+ext)
		err := os.WriteFile(filename, []byte("test"), filePerm)
		require.NoError(t, err)

		filenames = append(filenames, filename)
	}

	return filenames
}
