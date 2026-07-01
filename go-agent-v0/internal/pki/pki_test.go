package pki

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"testing"
	"time"
)

func TestKeyPEMRoundTrip(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	pemBytes, err := MarshalKeyPEM(key)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := ParseKeyPEM(pemBytes)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !got.Equal(key) {
		t.Error("parsed key does not equal the original")
	}
}

func TestSignCSRProducesVerifiableClientCert(t *testing.T) {
	ca, err := NewCA("test-ca")
	if err != nil {
		t.Fatalf("ca: %v", err)
	}
	key, _ := GenerateKey()
	csrPEM, err := MarshalCSRPEM(key, "agent-1")
	if err != nil {
		t.Fatalf("csr: %v", err)
	}
	certPEM, err := ca.SignCSR(csrPEM, time.Hour)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	leaf := parseLeaf(t, certPEM)
	if leaf.Subject.CommonName != "agent-1" {
		t.Errorf("CN = %q, want agent-1", leaf.Subject.CommonName)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		t.Errorf("issued cert does not verify for client auth: %v", err)
	}
}

func TestIssueServerCertHonorsHosts(t *testing.T) {
	ca, _ := NewCA("test-ca")
	certPEM, _, err := ca.IssueServerCert([]string{"example.test", "10.0.0.5"}, time.Hour)
	if err != nil {
		t.Fatalf("server cert: %v", err)
	}
	leaf := parseLeaf(t, certPEM)
	if err := leaf.VerifyHostname("example.test"); err != nil {
		t.Errorf("DNS SAN example.test missing: %v", err)
	}
	found := false
	for _, ip := range leaf.IPAddresses {
		if ip.Equal(net.ParseIP("10.0.0.5")) {
			found = true
		}
	}
	if !found {
		t.Error("IP SAN 10.0.0.5 missing from server cert")
	}
}

func parseLeaf(t *testing.T, certPEM []byte) *x509.Certificate {
	t.Helper()
	blk, _ := pem.Decode(certPEM)
	if blk == nil {
		t.Fatal("no PEM block in cert")
	}
	c, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	return c
}
