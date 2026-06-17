// Package pki holds the small certificate primitives the host agent's
// enrollment and transport need, plus the CA/signing helpers the dev-only mock
// platform uses. It is deliberately thin and stdlib-only (crypto/ecdsa,
// crypto/x509, crypto/tls): the agent owns every line of the code that handles
// its private key.
package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"
)

const (
	pemTypeKey  = "PRIVATE KEY"
	pemTypeCSR  = "CERTIFICATE REQUEST"
	pemTypeCert = "CERTIFICATE"
)

// GenerateKey creates a new ECDSA P-256 private key.
func GenerateKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

// MarshalKeyPEM encodes a private key as PKCS#8 PEM.
func MarshalKeyPEM(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("pki: marshal key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: pemTypeKey, Bytes: der}), nil
}

// ParseKeyPEM decodes a PKCS#8 PEM private key (used to build renewal CSRs from
// the key already on disk).
func ParseKeyPEM(p []byte) (*ecdsa.PrivateKey, error) {
	blk, _ := pem.Decode(p)
	if blk == nil {
		return nil, fmt.Errorf("pki: no PEM block in key")
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, fmt.Errorf("pki: parse key: %w", err)
	}
	key, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("pki: key is not ECDSA")
	}
	return key, nil
}

// MarshalCSRPEM builds a PEM-encoded certificate signing request for the given
// key and common name.
func MarshalCSRPEM(key *ecdsa.PrivateKey, commonName string) ([]byte, error) {
	tmpl := &x509.CertificateRequest{Subject: pkix.Name{CommonName: commonName}}
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		return nil, fmt.Errorf("pki: create CSR: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: pemTypeCSR, Bytes: der}), nil
}

// ClientConfig builds a TLS config for the agent: it presents the given client
// cert/key and trusts only the given CA (TLS 1.3 minimum).
func ClientConfig(certPEM, keyPEM, caPEM []byte) (*tls.Config, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("pki: load client keypair: %w", err)
	}
	pool, err := CAPool(caPEM)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// CAPool returns a cert pool containing the given PEM CA certificate(s).
func CAPool(caPEM []byte) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("pki: no valid certificate in CA PEM")
	}
	return pool, nil
}

// CA is a certificate authority that can sign agent CSRs and issue server
// certificates. It backs the dev-only mock platform and the tests.
type CA struct {
	Cert    *x509.Certificate
	Key     *ecdsa.PrivateKey
	CertPEM []byte
}

// NewCA creates a self-signed CA.
func NewCA(commonName string) (*CA, error) {
	key, err := GenerateKey()
	if err != nil {
		return nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("pki: create CA cert: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("pki: parse CA cert: %w", err)
	}
	return &CA{
		Cert:    cert,
		Key:     key,
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: pemTypeCert, Bytes: der}),
	}, nil
}

// SignCSR validates a PEM CSR and issues a client-auth certificate from it.
func (ca *CA) SignCSR(csrPEM []byte, ttl time.Duration) ([]byte, error) {
	blk, _ := pem.Decode(csrPEM)
	if blk == nil {
		return nil, fmt.Errorf("pki: no PEM block in CSR")
	}
	csr, err := x509.ParseCertificateRequest(blk.Bytes)
	if err != nil {
		return nil, fmt.Errorf("pki: parse CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("pki: CSR signature invalid: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      csr.Subject,
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(ttl),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, csr.PublicKey, ca.Key)
	if err != nil {
		return nil, fmt.Errorf("pki: sign CSR: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: pemTypeCert, Bytes: der}), nil
}

// IssueServerCert issues a server-auth certificate (and its key) for the given
// hosts (DNS names or IPs).
func (ca *CA) IssueServerCert(hosts []string, ttl time.Duration) (certPEM, keyPEM []byte, err error) {
	if len(hosts) == 0 {
		return nil, nil, fmt.Errorf("pki: no hosts for server cert")
	}
	key, err := GenerateKey()
	if err != nil {
		return nil, nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: hosts[0]},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(ttl),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, &key.PublicKey, ca.Key)
	if err != nil {
		return nil, nil, fmt.Errorf("pki: create server cert: %w", err)
	}
	keyPEM, err = MarshalKeyPEM(key)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: pemTypeCert, Bytes: der})
	return certPEM, keyPEM, nil
}

func randomSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("pki: serial: %w", err)
	}
	return serial, nil
}
