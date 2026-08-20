package certs

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadOrCreateAuthorityPersistsCA(t *testing.T) {
	dir := t.TempDir()
	a1, err := LoadOrCreateAuthority(dir)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := LoadOrCreateAuthority(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a1.FingerprintSHA256() != a2.FingerprintSHA256() {
		t.Fatalf("fingerprints differ: %s != %s", a1.FingerprintSHA256(), a2.FingerprintSHA256())
	}
	block, _ := pem.Decode(a1.CACertPEM())
	if block == nil {
		t.Fatal("missing PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if !cert.IsCA {
		t.Fatal("certificate is not a CA")
	}
}

func TestCertificateForHostContainsDNSName(t *testing.T) {
	a, err := LoadOrCreateAuthority(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := a.CertificateForHost("app.example.test")
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(cert.DNSNames) != 1 || cert.DNSNames[0] != "app.example.test" {
		t.Fatalf("DNSNames = %#v", cert.DNSNames)
	}
}

func TestLoadOrCreateAuthorityRecoversMismatchedPair(t *testing.T) {
	dir := t.TempDir()
	original, err := LoadOrCreateAuthority(dir)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := rsa.GenerateKey(rand.Reader, keyBits)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(otherKey),
	})
	if err := os.WriteFile(filepath.Join(dir, caKeyFile), keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	recovered, err := LoadOrCreateAuthority(dir)
	if err != nil {
		t.Fatalf("recover mismatched CA pair: %v", err)
	}
	if recovered.FingerprintSHA256() == original.FingerprintSHA256() {
		t.Fatal("recovered authority reused the corrupted certificate")
	}
	leaf, err := recovered.CertificateForHost("app.example.test")
	if err != nil {
		t.Fatal(err)
	}
	leafCert, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	caBlock, _ := pem.Decode(recovered.CACertPEM())
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	if _, err := leafCert.Verify(x509.VerifyOptions{
		Roots:     roots,
		DNSName:   "app.example.test",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Fatalf("verify recovered leaf certificate: %v", err)
	}
}

func TestLoadOrCreateAuthorityRestrictsKeyPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful for Windows ACLs")
	}
	dir := t.TempDir()
	if _, err := LoadOrCreateAuthority(dir); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, caKeyFile)
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("key permissions = %o, want 600", got)
	}
	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateAuthority(dir); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("reloaded key permissions = %o, want 600", got)
	}
}

func TestCertificateForHostVerifiesAgainstAuthority(t *testing.T) {
	a, err := LoadOrCreateAuthority(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := a.CertificateForHost("app.example.test")
	if err != nil {
		t.Fatal(err)
	}
	leafCert, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	caBlock, _ := pem.Decode(a.CACertPEM())
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	if _, err := leafCert.Verify(x509.VerifyOptions{
		Roots:     roots,
		DNSName:   "app.example.test",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Fatalf("verify leaf certificate: %v", err)
	}
}

func TestCertificateForHostReturnsIndependentCachedBytes(t *testing.T) {
	a, err := LoadOrCreateAuthority(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := a.CertificateForHost("app.example.test")
	if err != nil {
		t.Fatal(err)
	}
	first.Certificate[0][0] ^= 0xff
	second, err := a.CertificateForHost("app.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if first.Certificate[0][0] == second.Certificate[0][0] {
		t.Fatal("cached certificate bytes share mutable backing data")
	}
	if _, err := x509.ParseCertificate(second.Certificate[0]); err != nil {
		t.Fatalf("cached certificate was corrupted: %v", err)
	}
}
