package certs

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/idna"
)

const (
	caCertFile     = "ca.pem"
	caKeyFile      = "ca.key"
	keyBits        = 2048
	leafCacheLimit = 256
)

var errAuthorityKeyMismatch = errors.New("certificate authority certificate and key do not match")

// Authority owns the local CA key and the certificates it issues for hosts.
type Authority struct {
	certPEM []byte
	cert    *x509.Certificate
	key     *rsa.PrivateKey

	mu         sync.Mutex
	cache      map[string]tls.Certificate
	cacheOrder []string
	cacheLimit int
}

// LoadOrCreateAuthority loads the CA from dir, creating it when it is absent.
func LoadOrCreateAuthority(dir string) (*Authority, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("certificate authority directory is empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create certificate authority directory: %w", err)
	}
	unlock, err := acquireDirectoryLock(dir)
	if err != nil {
		return nil, err
	}
	defer unlock()

	certPath := filepath.Join(dir, caCertFile)
	keyPath := filepath.Join(dir, caKeyFile)
	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	if certErr == nil && keyErr == nil {
		authority, err := loadAuthority(certPEM, keyPEM, keyPath)
		if err == nil {
			return authority, nil
		}
		if !errors.Is(err, errAuthorityKeyMismatch) {
			return nil, err
		}
		if err := removeAuthorityPair(certPath, keyPath); err != nil {
			return nil, err
		}
	}
	if certErr != nil && !errors.Is(certErr, os.ErrNotExist) {
		return nil, fmt.Errorf("read certificate authority certificate: %w", certErr)
	}
	if keyErr != nil && !errors.Is(keyErr, os.ErrNotExist) {
		return nil, fmt.Errorf("read certificate authority key: %w", keyErr)
	}
	if certErr == nil || keyErr == nil {
		if certErr == nil {
			_ = os.Remove(certPath)
		}
		if keyErr == nil {
			_ = os.Remove(keyPath)
		}
	}

	authority, err := createAuthority()
	if err != nil {
		return nil, err
	}
	encodedKeyPEM, err := pemBlockForKey(authority.key)
	if err != nil {
		return nil, err
	}
	if err := persistAuthority(certPath, authority.certPEM, keyPath, encodedKeyPEM); err != nil {
		return nil, err
	}
	return authority, nil
}

func removeAuthorityPair(certPath, keyPath string) error {
	for _, path := range []string{certPath, keyPath} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove corrupt certificate authority file %q: %w", path, err)
		}
	}
	return nil
}

func persistAuthority(certPath string, certPEM []byte, keyPath string, keyPEM []byte) error {
	certTemp, err := writeTempFile(filepath.Dir(certPath), certPEM)
	if err != nil {
		return fmt.Errorf("stage certificate authority certificate: %w", err)
	}
	defer os.Remove(certTemp)
	keyTemp, err := writeTempFile(filepath.Dir(keyPath), keyPEM)
	if err != nil {
		return fmt.Errorf("stage certificate authority key: %w", err)
	}
	defer os.Remove(keyTemp)

	if err := os.Rename(keyTemp, keyPath); err != nil {
		return fmt.Errorf("persist certificate authority key: %w", err)
	}
	if err := os.Rename(certTemp, certPath); err != nil {
		_ = os.Remove(keyPath)
		return fmt.Errorf("persist certificate authority certificate: %w", err)
	}
	return nil
}

func writeTempFile(dir string, data []byte) (string, error) {
	temp, err := os.CreateTemp(dir, ".ca-*")
	if err != nil {
		return "", err
	}
	path := temp.Name()
	cleanup := true
	defer func() {
		_ = temp.Close()
		if cleanup {
			_ = os.Remove(path)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return "", err
	}
	if _, err := temp.Write(data); err != nil {
		return "", err
	}
	if err := temp.Sync(); err != nil {
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	cleanup = false
	return path, nil
}

func createAuthority() (*Authority, error) {
	key, err := rsa.GenerateKey(rand.Reader, keyBits)
	if err != nil {
		return nil, fmt.Errorf("generate certificate authority key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "BurpSuite Clone Local CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create certificate authority certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse certificate authority certificate: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return &Authority{
		certPEM: certPEM, cert: cert, key: key,
		cache: make(map[string]tls.Certificate), cacheLimit: leafCacheLimit,
	}, nil
}

func loadAuthority(certPEM, keyPEM []byte, keyPath string) (*Authority, error) {
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, errors.New("certificate authority certificate is not PEM")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse certificate authority certificate: %w", err)
	}
	if !cert.IsCA {
		return nil, errors.New("certificate authority certificate is not a CA")
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, errors.New("certificate authority key is not PEM")
	}
	key, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse certificate authority key: %w", err)
	}
	publicKey, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok || publicKey.N.Cmp(key.N) != 0 || publicKey.E != key.E {
		return nil, errAuthorityKeyMismatch
	}
	if err := os.Chmod(keyPath, 0o600); err != nil {
		return nil, fmt.Errorf("restrict certificate authority key permissions: %w", err)
	}
	return &Authority{
		certPEM: append([]byte(nil), certPEM...), cert: cert, key: key,
		cache: make(map[string]tls.Certificate), cacheLimit: leafCacheLimit,
	}, nil
}

func pemBlockForKey(key *rsa.PrivateKey) ([]byte, error) {
	der := x509.MarshalPKCS1PrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}), nil
}

func randomSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate certificate serial: %w", err)
	}
	return serial, nil
}

// FingerprintSHA256 returns the lowercase hexadecimal SHA-256 fingerprint of the CA.
func (a *Authority) FingerprintSHA256() string {
	fingerprint := sha256.Sum256(a.cert.Raw)
	return hex.EncodeToString(fingerprint[:])
}

// CACertPEM returns a copy of the CA certificate in PEM format.
func (a *Authority) CACertPEM() []byte {
	return append([]byte(nil), a.certPEM...)
}

// CertificateForHost returns a cached leaf certificate signed by the local CA.
func (a *Authority) CertificateForHost(host string) (tls.Certificate, error) {
	var err error
	host, err = normalizeCertificateHost(host)
	if err != nil {
		return tls.Certificate{}, err
	}

	a.mu.Lock()
	if certificate, ok := a.cache[host]; ok {
		a.mu.Unlock()
		return cloneTLSCertificate(certificate), nil
	}
	a.mu.Unlock()

	key, err := rsa.GenerateKey(rand.Reader, keyBits)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate host certificate key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, a.cert, &key.PublicKey, a.key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create host certificate: %w", err)
	}
	certificate := tls.Certificate{Certificate: [][]byte{der, a.cert.Raw}, PrivateKey: key}

	a.mu.Lock()
	defer a.mu.Unlock()
	if existing, ok := a.cache[host]; ok {
		return cloneTLSCertificate(existing), nil
	}
	limit := a.cacheLimit
	if limit <= 0 {
		limit = leafCacheLimit
	}
	if len(a.cacheOrder) >= limit {
		delete(a.cache, a.cacheOrder[0])
		a.cacheOrder = a.cacheOrder[1:]
	}
	a.cache[host] = certificate
	a.cacheOrder = append(a.cacheOrder, host)
	return cloneTLSCertificate(certificate), nil
}

func normalizeCertificateHost(host string) (string, error) {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return "", errors.New("certificate host is empty")
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), nil
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil || ascii == "" {
		return "", errors.New("certificate host is invalid")
	}
	return strings.ToLower(ascii), nil
}

func cloneTLSCertificate(certificate tls.Certificate) tls.Certificate {
	clone := certificate
	clone.Certificate = make([][]byte, len(certificate.Certificate))
	for i, der := range certificate.Certificate {
		clone.Certificate[i] = append([]byte(nil), der...)
	}
	return clone
}
