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
)

const (
	caCertFile = "ca.pem"
	caKeyFile  = "ca.key"
	keyBits    = 2048
)

// Authority owns the local CA key and the certificates it issues for hosts.
type Authority struct {
	certPEM []byte
	cert    *x509.Certificate
	key     *rsa.PrivateKey

	mu    sync.Mutex
	cache map[string]tls.Certificate
}

// LoadOrCreateAuthority loads the CA from dir, creating it when it is absent.
func LoadOrCreateAuthority(dir string) (*Authority, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("certificate authority directory is empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create certificate authority directory: %w", err)
	}

	certPath := filepath.Join(dir, caCertFile)
	keyPath := filepath.Join(dir, caKeyFile)
	certPEM, err := os.ReadFile(certPath)
	if err == nil {
		keyPEM, keyErr := os.ReadFile(keyPath)
		if keyErr != nil {
			return nil, fmt.Errorf("read certificate authority key: %w", keyErr)
		}
		return loadAuthority(certPEM, keyPEM, keyPath)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read certificate authority certificate: %w", err)
	}
	if _, err := os.Stat(keyPath); err == nil {
		return nil, errors.New("certificate authority key exists without certificate")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect certificate authority key: %w", err)
	}

	authority, err := createAuthority()
	if err != nil {
		return nil, err
	}
	keyPEM, err := pemBlockForKey(authority.key)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(certPath, authority.certPEM, 0o600); err != nil {
		return nil, fmt.Errorf("write certificate authority certificate: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, fmt.Errorf("write certificate authority key: %w", err)
	}
	return authority, nil
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
	return &Authority{certPEM: certPEM, cert: cert, key: key, cache: make(map[string]tls.Certificate)}, nil
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
	if err := os.Chmod(keyPath, 0o600); err != nil {
		return nil, fmt.Errorf("restrict certificate authority key permissions: %w", err)
	}
	return &Authority{certPEM: append([]byte(nil), certPEM...), cert: cert, key: key, cache: make(map[string]tls.Certificate)}, nil
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
	host = strings.TrimSpace(host)
	if host == "" {
		return tls.Certificate{}, errors.New("certificate host is empty")
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if certificate, ok := a.cache[host]; ok {
		return certificate, nil
	}

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
	a.cache[host] = certificate
	return certificate, nil
}
