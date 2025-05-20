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

type Storage interface {
	AccountSetup() (*Account, certcrypto.KeyType)
	AccountSave(*Account) error
	AccountLoad(crypto.PrivateKey) *Account
	GetPrivateKey(certcrypto.KeyType) crypto.PrivateKey
	ArchiveDomain(domain string) error
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

func (s *storage) AccountSetup() (*Account, certcrypto.KeyType) {
	keyType := certcrypto.EC384
	privateKey := s.GetPrivateKey(keyType)

	var account *Account
	if s.accountExists() {
		account = s.AccountLoad(privateKey)
	} else {
		account = &Account{Email: s.UserID, key: privateKey}
	}

	return account, keyType
}

func (s *storage) AccountLoad(privateKey crypto.PrivateKey) *Account {
	fileBytes, err := os.ReadFile(filepath.Join(s.rootUserPath, accountFileName))
	if err != nil {
		s.log.Error(fmt.Sprintf("could not load file for account %s: %v", s.UserID, err))
	}

	var account Account
	err = json.Unmarshal(fileBytes, &account)
	if err != nil {
		s.log.Error(fmt.Sprintf("could not parse file for account %s: %v", s.UserID, err))
	}

	account.key = privateKey

	if account.Registration == nil || account.Registration.Body.Status == "" {
		reg, err := s.tryRecoverRegistration(privateKey)
		if err != nil {
			s.log.Error(fmt.Sprintf("could not load account for %s. Registration is nil: %s", s.UserID, err))
		}

		account.Registration = reg
		err = s.AccountSave(&account)
		if err != nil {
			s.log.Error(fmt.Sprintf("could not save account for %s. Registration is nil: %s", s.UserID, err.Error()))
		}
	}

	return &account
}

func (s *storage) GetPrivateKey(keyType certcrypto.KeyType) crypto.PrivateKey {
	accKeyPath := filepath.Join(filepath.Join(s.rootUserPath, baseKeysFolderName), s.UserID+".key")

	if _, err := os.Stat(accKeyPath); os.IsNotExist(err) {
		s.log.Info(fmt.Sprintf("No key found for account %s. Generating a %s key.", s.UserID, keyType))

		privateKey, err := generatePrivateKey(accKeyPath, keyType)
		if err != nil {
			s.log.Error(fmt.Sprintf("Could not generate RSA private account key for account %s: %v", s.UserID, err))
		}

		s.log.Info(fmt.Sprintf("Saved key to %s", accKeyPath))

		return privateKey
	}

	privateKey, err := loadPrivateKey(accKeyPath)
	if err != nil {
		s.log.Error(fmt.Sprintf("Could not load RSA private key from file %s: %v", accKeyPath, err))
	}

	return privateKey
}

func (s *storage) ArchiveDomain(domain string) error {
	domain, err := s.sanitizedDomain(domain)
	if err != nil {
		return nil
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

func (s *storage) SaveResource(certRes *certificate.Resource) {
	domain := certRes.Domain

	// We store the certificate, private key and metadata in different files
	// as web servers would not be able to work with a combined file.
	err := s.WriteFile(domain, extCert, certRes.Certificate)
	if err != nil {
		s.log.Error("unable to save Certificate for domain", "domain", domain, "err", err)
	}

	if certRes.IssuerCertificate != nil {
		err = s.WriteFile(domain, extIssuer, certRes.IssuerCertificate)
		if err != nil {
			s.log.Error("Unable to save IssuerCertificate for domain", "domain", domain, "err", err)
		}
	}

	// if we were given a CSR, we don't know the private key
	if certRes.PrivateKey != nil {
		err = s.WriteCertificateFiles(domain, certRes)
		if err != nil {
			s.log.Error("Unable to save PrivateKey for domain", "domain", domain, "err", err)
		}
	} else if s.PEM || s.PFX {
		// we don't have the private key; can't write the .pem or .pfx file
		s.log.Error("Unable to save PEM or PFX without private key for domain %s. Are you using a CSR?", domain)
	}

	jsonBytes, err := json.MarshalIndent(certRes, "", "\t")
	if err != nil {
		s.log.Error("Unable to marshal CertResource for domain", "domain", domain, "err", err)
	}

	err = s.WriteFile(domain, extResource, jsonBytes)
	if err != nil {
		s.log.Error("Unable to save CertResource for domain", "domain", domain, "err", err)
	}
}

func (s *storage) ReadResource(domain string) certificate.Resource {
	raw, err := s.ReadFile(domain, extResource)
	if err != nil {
		s.log.Error("error while loading the meta data for domain %s\n\t%v", domain, err)
	}

	var resource certificate.Resource
	if err = json.Unmarshal(raw, &resource); err != nil {
		s.log.Error("error while marshaling the meta data for domain %s\n\t%v", domain, err)
	}

	return resource
}

func (s *storage) ReadFile(domain, extension string) ([]byte, error) {
	filename, err := s.GetFileName(domain, extension)
	if err != nil {
		return nil, err
	}

	return os.ReadFile(filename)
}

func (s *storage) GetFileName(domain, extension string) (string, error) {
	rootPath := filepath.Join(s.RootPath, baseCertificatesRootFolderName)

	filename, err := s.sanitizedDomain(domain)
	if err != nil {
		return "", err
	}

	return filepath.Join(rootPath, filename+extension), nil
}

func (s *storage) WriteFile(domain, extension string, data []byte) error {
	baseFileName := s.Filename

	if baseFileName == "" {
		var err error
		baseFileName, err = s.sanitizedDomain(domain)
		if err != nil {
			return err
		}
	}

	rootPath := filepath.Join(s.RootPath, baseCertificatesRootFolderName)
	filePath := filepath.Join(rootPath, baseFileName+extension)

	return os.WriteFile(filePath, data, filePerm)
}

func (s *storage) WriteCertificateFiles(domain string, certRes *certificate.Resource) error {
	err := s.WriteFile(domain, extKey, certRes.PrivateKey)
	if err != nil {
		return fmt.Errorf("unable to save key file: %w", err)
	}

	if s.PEM {
		err = s.WriteFile(domain, extPem, bytes.Join([][]byte{certRes.Certificate, certRes.PrivateKey}, nil))
		if err != nil {
			return fmt.Errorf("unable to save PEM file: %w", err)
		}
	}

	if s.PFX {
		err = s.WritePFXFile(domain, certRes)
		if err != nil {
			return fmt.Errorf("unable to save PFX file: %w", err)
		}
	}

	return nil
}

func (s *storage) WritePFXFile(domain string, certRes *certificate.Resource) error {
	certPemBlock, _ := pem.Decode(certRes.Certificate)
	if certPemBlock == nil {
		return fmt.Errorf("unable to parse Certificate for domain %s", domain)
	}

	cert, err := x509.ParseCertificate(certPemBlock.Bytes)
	if err != nil {
		return fmt.Errorf("unable to load Certificate for domain %s: %w", domain, err)
	}

	certChain, err := getCertificateChain(certRes)
	if err != nil {
		return fmt.Errorf("unable to get certificate chain for domain %s: %w", domain, err)
	}

	keyPemBlock, _ := pem.Decode(certRes.PrivateKey)
	if keyPemBlock == nil {
		return fmt.Errorf("unable to parse PrivateKey for domain %s", domain)
	}

	var privateKey crypto.Signer
	var keyErr error

	switch keyPemBlock.Type {
	case "RSA PRIVATE KEY":
		privateKey, keyErr = x509.ParsePKCS1PrivateKey(keyPemBlock.Bytes)
		if keyErr != nil {
			return fmt.Errorf("unable to load RSA PrivateKey for domain %s: %w", domain, keyErr)
		}
	case "EC PRIVATE KEY":
		privateKey, keyErr = x509.ParseECPrivateKey(keyPemBlock.Bytes)
		if keyErr != nil {
			return fmt.Errorf("unable to load EC PrivateKey for domain %s: %w", domain, keyErr)
		}
	default:
		return fmt.Errorf("unsupported PrivateKey type '%s' for domain %s", keyPemBlock.Type, domain)
	}

	encoder, err := getPFXEncoder(s.PFXFormat)
	if err != nil {
		return fmt.Errorf("PFX encoder: %w", err)
	}

	pfxBytes, err := encoder.Encode(privateKey, cert, certChain, s.PFXPassword)
	if err != nil {
		return fmt.Errorf("unable to encode PFX data for domain %s: %w", domain, err)
	}

	return s.WriteFile(domain, extPfx, pfxBytes)
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

	keyBlock, _ := pem.Decode(keyBytes)

	switch keyBlock.Type {
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	case "EC PRIVATE KEY":
		return x509.ParseECPrivateKey(keyBlock.Bytes)
	}

	return nil, errors.New("unknown private key type")
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
func (s *storage) sanitizedDomain(domain string) (string, error) {
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
		return nil, fmt.Errorf("invalid PFX format: %s", pfxFormat)
	}

	return encoder, nil
}

func getCertificateChain(certRes *certificate.Resource) ([]*x509.Certificate, error) {
	chainCertPemBlock, rest := pem.Decode(certRes.IssuerCertificate)
	if chainCertPemBlock == nil {
		return nil, errors.New("unable to parse Issuer Certificate")
	}

	var certChain []*x509.Certificate
	for chainCertPemBlock != nil {
		chainCert, err := x509.ParseCertificate(chainCertPemBlock.Bytes)
		if err != nil {
			return nil, fmt.Errorf("unable to parse Chain Certificate: %w", err)
		}

		certChain = append(certChain, chainCert)
		chainCertPemBlock, rest = pem.Decode(rest) // Try decoding the next pem block
	}

	return certChain, nil
}
