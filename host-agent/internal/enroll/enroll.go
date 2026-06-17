// Package enroll manages the agent's mTLS client identity. On first run it
// generates a key pair locally (the private key never leaves the host), sends a
// CSR plus a one-time token to the platform's enrollment endpoint, and stores
// the returned certificate. The returned *Identity can renew itself in place
// (a fresh CSR over its current mTLS identity, before expiry), so the transport
// keeps using one *tls.Config across rotations. If the stored certificate has
// expired and a token is available, EnsureIdentity re-enrolls.
package enroll

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/config"
	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/pki"
)

const (
	keyFile  = "client.key"
	certFile = "client.crt"
	caFile   = "ca.crt"
)

type csrRequest struct {
	CSRPEM  string `json:"csr_pem"`
	AgentID string `json:"agent_id"`
}

type certResponse struct {
	CertPEM string `json:"cert_pem"`
	CAPEM   string `json:"ca_pem"`
}

// Identity is the agent's mTLS client identity: a private key (never leaves the
// host), the current certificate, and the pinned platform CA. It is safe for
// concurrent use; Renew swaps the certificate in place while the transport keeps
// the same *tls.Config (which fetches the live cert per handshake).
type Identity struct {
	mu       sync.RWMutex
	key      *ecdsa.PrivateKey
	keyPEM   []byte
	cert     tls.Certificate
	leaf     *x509.Certificate
	caPool   *x509.CertPool
	certPath string
}

// EnsureIdentity loads the stored identity, enrolling first if there is none —
// or re-enrolling if the stored certificate has expired and a token is set.
func EnsureIdentity(ctx context.Context, cfg config.Config) (*Identity, error) {
	keyPath := filepath.Join(cfg.CertDir, keyFile)
	certPath := filepath.Join(cfg.CertDir, certFile)
	caPath := filepath.Join(cfg.CertDir, caFile)

	if fileExists(keyPath) && fileExists(certPath) && fileExists(caPath) {
		certPEM, keyPEM, caPEM, err := readPEMs(certPath, keyPath, caPath)
		if err != nil {
			return nil, err
		}
		id, err := newIdentity(certPEM, keyPEM, caPEM, certPath)
		if err != nil {
			return nil, err
		}
		if time.Now().Before(id.NotAfter()) {
			return id, nil // stored cert still valid
		}
		// Expired — re-enroll if we can, otherwise the operator must re-onboard.
		if cfg.EnrollToken == "" {
			return nil, fmt.Errorf("enroll: stored certificate expired at %s and AGENT_ENROLL_TOKEN is empty; re-onboarding required",
				id.NotAfter().UTC().Format(time.RFC3339))
		}
	}

	// Enroll. The platform CA must be present out-of-band so we can trust the
	// enrollment endpoint (no trust-on-first-use).
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("enroll: platform CA not found at %s (place it there to enroll): %w", caPath, err)
	}
	if cfg.EnrollToken == "" {
		return nil, fmt.Errorf("enroll: no client certificate and AGENT_ENROLL_TOKEN is empty; cannot enroll")
	}
	if err := cfg.EnsureCertDir(); err != nil {
		return nil, err
	}

	key, err := pki.GenerateKey()
	if err != nil {
		return nil, err
	}
	keyPEM, err := pki.MarshalKeyPEM(key)
	if err != nil {
		return nil, err
	}
	csrPEM, err := pki.MarshalCSRPEM(key, agentID())
	if err != nil {
		return nil, err
	}

	pool, err := pki.CAPool(caPEM)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13}},
	}
	certPEM, err := postCSR(ctx, client, cfg.EnrollEndpoint, cfg.EnrollToken, csrPEM)
	if err != nil {
		return nil, err
	}

	// Persist the private key (0600) before the cert. The CA is already on disk.
	if err := writeFileAtomic(keyPath, keyPEM, 0o600); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(certPath, certPEM, 0o644); err != nil {
		return nil, err
	}
	return newIdentity(certPEM, keyPEM, caPEM, certPath)
}

// TLSConfig returns a config that presents the agent's *current* certificate
// (fetched per handshake, so renewals take effect without rebuilding it) and
// trusts only the pinned platform CA.
func (i *Identity) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    i.caPool,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			i.mu.RLock()
			defer i.mu.RUnlock()
			c := i.cert
			return &c, nil
		},
	}
}

// NotAfter is the current certificate's expiry.
func (i *Identity) NotAfter() time.Time {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.leaf.NotAfter
}

// NeedsRenewal reports whether the certificate expires within the given window.
func (i *Identity) NeedsRenewal(before time.Duration) bool {
	return time.Until(i.NotAfter()) <= before
}

// Renew requests a fresh certificate for the existing key over the current mTLS
// identity (no token), swaps it in, and rewrites client.crt.
func (i *Identity) Renew(ctx context.Context, cfg config.Config) error {
	i.mu.RLock()
	key := i.key
	keyPEM := i.keyPEM
	i.mu.RUnlock()

	csrPEM, err := pki.MarshalCSRPEM(key, agentID())
	if err != nil {
		return err
	}
	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: i.TLSConfig()},
	}
	certPEM, err := postCSR(ctx, client, cfg.RenewEndpoint, "", csrPEM) // mTLS authenticates; no token
	if err != nil {
		return fmt.Errorf("renew: %w", err)
	}
	newCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return fmt.Errorf("renew: load renewed keypair: %w", err)
	}
	leaf, err := x509.ParseCertificate(newCert.Certificate[0])
	if err != nil {
		return fmt.Errorf("renew: parse renewed cert: %w", err)
	}

	i.mu.Lock()
	i.cert = newCert
	i.leaf = leaf
	i.mu.Unlock()

	return writeFileAtomic(i.certPath, certPEM, 0o644)
}

func newIdentity(certPEM, keyPEM, caPEM []byte, certPath string) (*Identity, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("enroll: load keypair: %w", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("enroll: parse certificate: %w", err)
	}
	key, err := pki.ParseKeyPEM(keyPEM)
	if err != nil {
		return nil, err
	}
	pool, err := pki.CAPool(caPEM)
	if err != nil {
		return nil, err
	}
	return &Identity{key: key, keyPEM: keyPEM, cert: cert, leaf: leaf, caPool: pool, certPath: certPath}, nil
}

// postCSR sends a CSR to endpoint (with an optional bearer token) and returns
// the issued certificate PEM.
func postCSR(ctx context.Context, client *http.Client, endpoint, token string, csrPEM []byte) ([]byte, error) {
	body, err := json.Marshal(csrRequest{CSRPEM: string(csrPEM), AgentID: agentID()})
	if err != nil {
		return nil, fmt.Errorf("enroll: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("enroll: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("enroll: POST %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("enroll: %s returned %s: %s", endpoint, resp.Status, bytes.TrimSpace(respBody))
	}
	var cr certResponse
	if err := json.Unmarshal(respBody, &cr); err != nil {
		return nil, fmt.Errorf("enroll: decode response: %w", err)
	}
	if cr.CertPEM == "" {
		return nil, fmt.Errorf("enroll: response contained no certificate")
	}
	return []byte(cr.CertPEM), nil
}

func agentID() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "unknown-host"
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func readPEMs(certPath, keyPath, caPath string) (cert, key, ca []byte, err error) {
	if cert, err = os.ReadFile(certPath); err != nil {
		return
	}
	if key, err = os.ReadFile(keyPath); err != nil {
		return
	}
	ca, err = os.ReadFile(caPath)
	return
}

func writeFileAtomic(path string, data []byte, perm fs.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return fmt.Errorf("enroll: write %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("enroll: commit %s: %w", filepath.Base(path), err)
	}
	return nil
}
