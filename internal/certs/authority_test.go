package certs

import (
	"crypto/x509"
	"encoding/pem"
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
