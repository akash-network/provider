package certissuer

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
	"golang.org/x/net/idna"
	"software.sslmate.com/src/go-pkcs12"

	"github.com/tendermint/tendermint/libs/log"
)

const (
	baseAccountsRootFolderName     = "accounts"
	baseCertificatesRootFolderName = "certificates"
	baseArchivesRootFolderName     = "archives"
	baseKeysFolderName             = "keys"
	accountFileName                = "account.json"
)

const (
	extIssuer   = ".issuer.crt"
	extCert     = ".crt"
	extKey      = ".key"
	extPem      = ".pem"
	extPfx      = ".pfx"
	extResource = ".json"
)

const filePerm os.FileMode = 0o600

var (
	ErrStorageUnknownPrivateKeyType = errors.New("cert storage: invalid private key type")
	ErrStorageParse                 = errors.New("cert storage: parse")
	ErrStorageParsePrivateKey       = errors.New("cert storage: unable to parse private key")
	ErrStorageParseCertificate      = errors.New("cert storage: unable to parse certificate")
	ErrStorageUnsupportedKeyType    = errors.New("cert storage: unsupported private key type")
	ErrStorageMissingPrivateKey     = errors.New("cert storage: missing private key")
	ErrStorageInvalidPFXFormat      = errors.New("cert storage: invalid PFX format")
)

type StorageConfig struct {
	CADirURL    string
	UserID      string
	RootPath    string
	PEM         bool
	PFX         bool
	PFXPassword string
	PFXFormat   string
	// Deprecated
	Filename string
}

type ResourcesInfo struct {
	Domain         string `json:"domain"`
	CertFile       string `json:",omitempty"`
	CertIssuerFile string `json:",omitempty"`
	KeyFile        string `json:",omitempty"`
	PemFile        string `json:",omitempty"`
	PfxFile        string `json:",omitempty"`
}

type Reader interface {
	ReadResource(domain string) (*certificate.Resource, ResourcesInfo, error)
}

type Storage interface {
	AccountSetup() (*Account, certcrypto.KeyType, error)
	AccountSave(*Account) error
	AccountLoad(crypto.PrivateKey) (*Account, error)
	GetPrivateKey(certcrypto.KeyType) (crypto.PrivateKey, error)
	ArchiveDomain(string) error
	SaveResource(resource *certificate.Resource) (ResourcesInfo, error)
	Reader
}

type getFileName func(string, string) (string, error)

type writerRollback struct {
	extensions []string
}

func (wr *writerRollback) addExt(ext string) {
	wr.extensions = append(wr.extensions, ext)
}

func (wr *writerRollback) cleanup(get getFileName, domain string) {
	for _, ext := range wr.extensions {
		fileName, err := get(domain, ext)
		if err == nil {
			_ = os.Remove(fileName)
		}
	}
}

type writeOptions struct {
	rollback        *writerRollback
	rollbackStacked bool
}

type WriteOption func(*writeOptions) error

func withWriterRollback(val *writerRollback) WriteOption {
	return func(options *writeOptions) error {
		options.rollback = val
		options.rollbackStacked = true

		return nil
	}
}

func parseWriteOpions(opts ...WriteOption) (*writeOptions, error) {
	res := &writeOptions{}

	for _, opt := range opts {
		if err := opt(res); err != nil {
			return nil, err
		}
	}

	if res.rollback == nil {
		res.rollback = &writerRollback{}
	}

	return res, nil
}

// Storage A storage for account and certificates data
//
// rootPath:
//
//	./.certissuer/accounts/
//	     │      └── root accounts directory
//	     └── "path" option
//
// rootUserPath:
//
//	./.certissuer/accounts/localhost_14000/hubert@hubert.com/
//	     │      │             │             └── userID ("email" option)
//	     │      │             └── CA server ("server" option)
//	     │      └── root accounts directory
//	     └── "path" option
//
// keysPath:
//
//	./.certissuer/accounts/localhost_14000/hubert@hubert.com/keys/
//	     │      │             │             │           └── root keys directory
//	     │      │             │             └── userID ("email" option)
//	     │      │             └── CA server ("server" option)
//	     │      └── root accounts directory
//	     └── "path" option
//
// accountFilePath:
//
//	./.certissuer/accounts/localhost_14000/hubert@hubert.com/account.json
//	     │      │             │             │             └── account file
//	     │      │             │             └── userID ("email" option)
//	     │      │             └── CA server ("server" option)
//	     │      └── root accounts directory
//	     └── "path" option
//
//	./.certissuer/certificates/
//	     │      └── root certificates directory
//	     └── "path" option
//
// archivePath:
//
//	./.certissuer/archives/
//	     │      └── archived certificates directory
//	     └── "path" option
type storage struct {
	StorageConfig
	rootUserPath string
	log          log.Logger
}

// NewStorage Creates a new AccountsStorage.
func NewStorage(log log.Logger, cfg StorageConfig) (Storage, error) {
	serverURL, err := url.Parse(cfg.CADirURL)
	if err != nil {
		log.Error(err.Error())
		return nil, err
	}

	accountsPath := filepath.Join(cfg.RootPath, baseAccountsRootFolderName)
	serverPath := strings.NewReplacer(":", "_", "/", string(os.PathSeparator)).Replace(serverURL.Host)
	rootUserPath := filepath.Join(accountsPath, serverPath, cfg.UserID)

	st := &storage{
		StorageConfig: cfg,
		rootUserPath:  rootUserPath,
		log:           log.With("component", "storage"),
	}

	if err = st.ensureDirStructure(); err != nil {
		return nil, err
	}

	return st, nil
}

func (s *storage) accountExists() bool {
	accountFile := filepath.Join(s.rootUserPath, accountFileName)
	if _, err := os.Stat(accountFile); os.IsNotExist(err) {
		return false
	} else if err != nil {
		s.log.Error(err.Error())
	}

	return true
}

func (s *storage) AccountSave(account *Account) error {
	jsonBytes, err := json.MarshalIndent(account, "", "\t")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(s.rootUserPath, accountFileName), jsonBytes, filePerm)
}

func (s *storage) AccountSetup() (*Account, certcrypto.KeyType, error) {
	keyType := certcrypto.EC384
	privateKey, err := s.GetPrivateKey(keyType)
	if err != nil {
		return nil, "", err
	}

	var account *Account
	if s.accountExists() {
		account, err = s.AccountLoad(privateKey)
		if err != nil {
			return nil, "", err
		}
	} else {
		account = &Account{Email: s.UserID, key: privateKey}
	}

	return account, keyType, nil
}

func (s *storage) AccountLoad(privateKey crypto.PrivateKey) (*Account, error) {
	fileBytes, err := os.ReadFile(filepath.Join(s.rootUserPath, accountFileName))
	if err != nil {
		return nil, err
	}

	var account Account
	err = json.Unmarshal(fileBytes, &account)
	if err != nil {
		return nil, err
	}

	account.key = privateKey

	if account.Registration == nil || account.Registration.Body.Status == "" {
		reg, err := s.tryRecoverRegistration(privateKey)
		if err != nil {
			return nil, err
		}

		account.Registration = reg
		err = s.AccountSave(&account)
		if err != nil {
			return nil, err
		}
	}

	return &account, nil
}

func (s *storage) GetPrivateKey(keyType certcrypto.KeyType) (crypto.PrivateKey, error) {
	accKeyPath := filepath.Join(filepath.Join(s.rootUserPath, baseKeysFolderName), s.UserID+".key")

	if _, err := os.Stat(accKeyPath); os.IsNotExist(err) {
		s.log.Info(fmt.Sprintf("no key found for account %s. generating a %s key.", s.UserID, keyType))

		privateKey, err := generatePrivateKey(accKeyPath, keyType)
		if err != nil {
			return nil, err
		}

		s.log.Info(fmt.Sprintf("saved key to %s", accKeyPath))

		return privateKey, nil
	}

	privateKey, err := loadPrivateKey(accKeyPath)
	if err != nil {
		return nil, err
	}

	return privateKey, nil
}

func (s *storage) ArchiveDomain(domain string) error {
	domain, err := sanitizedDomain(domain)
	if err != nil {
		return fmt.Errorf("failed to sanitize domain: %w", err)
	}

	baseFilename := filepath.Join(filepath.Join(s.RootPath, baseCertificatesRootFolderName), domain)

	matches, err := filepath.Glob(baseFilename + ".*")
	if err != nil {
		return err
	}

	for _, oldFile := range matches {
		if strings.TrimSuffix(oldFile, filepath.Ext(oldFile)) != baseFilename && oldFile != baseFilename+extIssuer {
			continue
		}

		date := strconv.FormatInt(time.Now().Unix(), 10)
		filename := date + "." + filepath.Base(oldFile)
		newFile := filepath.Join(s.RootPath, baseArchivesRootFolderName, filename)

		err = os.Rename(oldFile, newFile)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *storage) SaveResource(certRes *certificate.Resource) (ResourcesInfo, error) {
	domain := certRes.Domain

	var err error
	rollback := &writerRollback{}
	defer func() {
		if err != nil {
			rollback.cleanup(s.getFileName, domain)
		}
	}()

	info := ResourcesInfo{}

	// if we were given a CSR, we don't know the private key
	if certRes.PrivateKey != nil {
		info.KeyFile, err = s.writeFile(domain, extKey, certRes.PrivateKey, withWriterRollback(rollback))
		if err != nil {
			return ResourcesInfo{}, fmt.Errorf("unable to save key file: %w", err)
		}

		if s.PEM {
			info.PemFile, err = s.writeFile(domain, extPem, bytes.Join([][]byte{certRes.Certificate, certRes.PrivateKey}, nil), withWriterRollback(rollback))
			if err != nil {
				return ResourcesInfo{}, fmt.Errorf("unable to save PEM file: %w", err)
			}
		}

		if s.PFX {
			info.PfxFile, err = s.writePFXFile(domain, certRes, withWriterRollback(rollback))
			if err != nil {
				return ResourcesInfo{}, fmt.Errorf("unable to save PFX file: %w", err)
			}
		}
	} else if s.PEM || s.PFX {
		// we don't have the private key; can't write the .pem or .pfx file
		return ResourcesInfo{}, fmt.Errorf("%w for domain %s", ErrStorageMissingPrivateKey, certRes.Domain)
	}

	// We store the certificate, private key and metadata in different files
	// as web servers would not be able to work with a combined file.
	info.CertFile, err = s.writeFile(domain, extCert, certRes.Certificate, withWriterRollback(rollback))
	if err != nil {
		return ResourcesInfo{}, err
	}

	if certRes.IssuerCertificate != nil {
		info.CertIssuerFile, err = s.writeFile(domain, extIssuer, certRes.IssuerCertificate, withWriterRollback(rollback))
		if err != nil {
			return ResourcesInfo{}, err
		}
	}

	jsonBytes, err := json.MarshalIndent(certRes, "", "\t")
	if err != nil {
		return ResourcesInfo{}, err
	}

	_, err = s.writeFile(domain, extResource, jsonBytes, withWriterRollback(rollback))
	if err != nil {
		return ResourcesInfo{}, err
	}

	return info, nil
}

func (s *storage) ReadResource(domain string) (*certificate.Resource, ResourcesInfo, error) {
	raw, _, err := s.readFile(domain, extResource)
	if err != nil {
		return nil, ResourcesInfo{}, err
	}

	resource := &certificate.Resource{
		Domain: domain,
	}

	info := ResourcesInfo{
		Domain: domain,
	}
	if err = json.Unmarshal(raw, &resource); err != nil {
		return nil, ResourcesInfo{}, err
	}

	resource.Certificate, info.CertFile, err = s.readFile(domain, extCert)
	if err != nil {
		return nil, ResourcesInfo{}, err
	}

	resource.PrivateKey, info.KeyFile, err = s.readFile(domain, extKey)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, ResourcesInfo{}, nil
	}

	resource.IssuerCertificate, info.CertIssuerFile, err = s.readFile(domain, extIssuer)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, ResourcesInfo{}, nil
	}

	return resource, info, nil
}

func (s *storage) getFileName(domain string, extension string) (string, error) {
	rootPath := filepath.Join(s.RootPath, baseCertificatesRootFolderName)

	filename, err := sanitizedDomain(domain)
	if err != nil {
		return "", err
	}

	return filepath.Join(rootPath, filename+extension), nil
}

func (s *storage) readFile(domain string, extension string) ([]byte, string, error) {
	filename, err := s.getFileName(domain, extension)
	if err != nil {
		return nil, "", err
	}

	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, "", err
	}

	return data, filename, nil
}

func (s *storage) writeFile(domain string, extension string, data []byte, opts ...WriteOption) (string, error) {
	baseFileName := s.Filename

	wOpts, err := parseWriteOpions(opts...)
	if err != nil {
		return "", err
	}

	defer func() {
		if !wOpts.rollbackStacked {
			wOpts.rollback.cleanup(s.getFileName, domain)
		}
	}()

	if baseFileName == "" {
		baseFileName, err = sanitizedDomain(domain)
		if err != nil {
			return "", err
		}
	}

	rootPath := filepath.Join(s.RootPath, baseCertificatesRootFolderName)
	filePath := filepath.Join(rootPath, baseFileName+extension)

	err = os.WriteFile(filePath, data, filePerm)
	if err != nil {
		return "", err
	}

	wOpts.rollback.addExt(extension)

	return filePath, nil
}

func (s *storage) writePFXFile(domain string, certRes *certificate.Resource, opts ...WriteOption) (string, error) {
	certPemBlock, _ := pem.Decode(certRes.Certificate)
	if certPemBlock == nil {
		return "", fmt.Errorf("%w for domain %s", ErrStorageParseCertificate, domain)
	}

	cert, err := x509.ParseCertificate(certPemBlock.Bytes)
	if err != nil {
		return "", fmt.Errorf("unable to load Certificate for domain %s: %w", domain, err)
	}

	certChain, err := getCertificateChain(certRes)
	if err != nil {
		return "", fmt.Errorf("unable to get certificate chain for domain %s: %w", domain, err)
	}

	keyPemBlock, _ := pem.Decode(certRes.PrivateKey)
	if keyPemBlock == nil {
		return "", fmt.Errorf("%w for domain %s", ErrStorageParsePrivateKey, domain)
	}

	var privateKey crypto.Signer
	var keyErr error

	switch keyPemBlock.Type {
	case "RSA PRIVATE KEY":
		privateKey, keyErr = x509.ParsePKCS1PrivateKey(keyPemBlock.Bytes)
		if keyErr != nil {
			return "", fmt.Errorf("unable to load RSA PrivateKey for domain %s: %w", domain, keyErr)
		}
	case "EC PRIVATE KEY":
		privateKey, keyErr = x509.ParseECPrivateKey(keyPemBlock.Bytes)
		if keyErr != nil {
			return "", fmt.Errorf("unable to load EC PrivateKey for domain %s: %w", domain, keyErr)
		}
	default:
		return "", fmt.Errorf("%w '%s' for domain %s", ErrStorageUnsupportedKeyType, keyPemBlock.Type, domain)
	}

	encoder, err := getPFXEncoder(s.PFXFormat)
	if err != nil {
		return "", fmt.Errorf("PFX encoder: %w", err)
	}

	pfxBytes, err := encoder.Encode(privateKey, cert, certChain, s.PFXPassword)
	if err != nil {
		return "", fmt.Errorf("unable to encode PFX data for domain %s: %w", domain, err)
	}

	return s.writeFile(domain, extPfx, pfxBytes, opts...)
}

func generatePrivateKey(file string, keyType certcrypto.KeyType) (crypto.PrivateKey, error) {
	privateKey, err := certcrypto.GeneratePrivateKey(keyType)
	if err != nil {
		return nil, err
	}

	certOut, err := os.Create(file)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = certOut.Close()
	}()

	pemKey := certcrypto.PEMBlock(privateKey)
	err = pem.Encode(certOut, pemKey)
	if err != nil {
		return nil, err
	}

	return privateKey, nil
}

func loadPrivateKey(file string) (crypto.PrivateKey, error) {
	keyBytes, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	keyBlock, rem := pem.Decode(keyBytes)
	if len(rem) > 0 {
		return nil, fmt.Errorf("%w: pem file contains remaining data", ErrStorageParse)
	}

	switch keyBlock.Type {
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	case "EC PRIVATE KEY":
		return x509.ParseECPrivateKey(keyBlock.Bytes)
	}

	return nil, fmt.Errorf("%w: %s", ErrStorageUnknownPrivateKeyType, keyBlock.Type)
}

func (s *storage) tryRecoverRegistration(privateKey crypto.PrivateKey) (*registration.Resource, error) {
	// Couldn't load the account but got a key. Try to look the account up.
	config := lego.NewConfig(&Account{key: privateKey})
	config.CADirURL = s.CADirURL
	config.UserAgent = "provider-services"

	client, err := lego.NewClient(config)
	if err != nil {
		return nil, err
	}

	reg, err := client.Registration.ResolveAccountByKey()
	if err != nil {
		return nil, err
	}

	return reg, nil
}

func createFolderIfNotExists(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return os.MkdirAll(path, 0o700)
	} else if err != nil {
		return err
	}

	return nil
}

func (s *storage) ensureDirStructure() error {
	err := createFolderIfNotExists(s.rootUserPath)
	if err != nil {
		return err
	}

	err = createFolderIfNotExists(filepath.Join(s.rootUserPath, baseKeysFolderName))
	if err != nil {
		return err
	}

	err = createFolderIfNotExists(filepath.Join(s.RootPath, baseCertificatesRootFolderName))
	if err != nil {
		return err
	}

	err = createFolderIfNotExists(filepath.Join(s.RootPath, baseArchivesRootFolderName))
	if err != nil {
		return err
	}

	return nil
}

// sanitizedDomain Make sure no funny chars are in the cert names (like wildcards ;)).
func sanitizedDomain(domain string) (string, error) {
	safe, err := idna.ToASCII(strings.NewReplacer(":", "-", "*", "_").Replace(domain))
	if err != nil {
		return "", err
	}

	return safe, nil
}

func getPFXEncoder(pfxFormat string) (*pkcs12.Encoder, error) {
	var encoder *pkcs12.Encoder
	switch pfxFormat {
	case "SHA256":
		encoder = pkcs12.Modern2023
	case "DES":
		encoder = pkcs12.LegacyDES
	case "RC2":
		encoder = pkcs12.LegacyRC2
	default:
		return nil, fmt.Errorf("%w: %s", ErrStorageInvalidPFXFormat, pfxFormat)
	}

	return encoder, nil
}

func getCertificateChain(certRes *certificate.Resource) ([]*x509.Certificate, error) {
	chainCertPemBlock, rest := pem.Decode(certRes.IssuerCertificate)
	if chainCertPemBlock == nil {
		return nil, fmt.Errorf("%w: unable to parse Issuer Certificate", ErrStorageParse)
	}

	var certChain []*x509.Certificate
	for chainCertPemBlock != nil {
		chainCert, err := x509.ParseCertificate(chainCertPemBlock.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%w: unable to parse Chain Certificate: %w", ErrStorageParse, err)
		}

		certChain = append(certChain, chainCert)
		chainCertPemBlock, rest = pem.Decode(rest) // Try decoding the next pem block
	}

	return certChain, nil
}
