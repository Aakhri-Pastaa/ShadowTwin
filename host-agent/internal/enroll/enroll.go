// Package enroll obtains the mTLS client identity the transport uses. On first
// run it generates a key pair locally (the private key never leaves the host),
// sends a CSR plus a one-time token to the platform's enrollment endpoint, and
// stores the returned certificate. On later runs it loads the stored identity.
// Certificate renewal and revocation are a later slice.
package enroll

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/config"
	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/pki"
)

const (
	keyFile  = "client.key"
	certFile = "client.crt"
	caFile   = "ca.crt"
)

type enrollRequest struct {
	CSRPEM  string `json:"csr_pem"`
	AgentID string `json:"agent_id"`
}

type enrollResponse struct {
	CertPEM string `json:"cert_pem"`
	CAPEM   string `json:"ca_pem"`
}

// EnsureIdentity returns a ready mTLS client config, enrolling first if needed.
func EnsureIdentity(ctx context.Context, cfg config.Config) (*tls.Config, error) {
	keyPath := filepath.Join(cfg.CertDir, keyFile)
	certPath := filepath.Join(cfg.CertDir, certFile)
	caPath := filepath.Join(cfg.CertDir, caFile)

	// Fast path: already enrolled.
	if fileExists(keyPath) && fileExists(certPath) && fileExists(caPath) {
		certPEM, keyPEM, caPEM, err := readIdentity(certPath, keyPath, caPath)
		if err != nil {
			return nil, err
		}
		return pki.ClientConfig(certPEM, keyPEM, caPEM)
	}

	// Need to enroll. The platform CA must be present out-of-band so we can trust
	// the enrollment endpoint (no trust-on-first-use).
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

	certPEM, err := requestCert(ctx, cfg, csrPEM, caPEM)
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
	return pki.ClientConfig(certPEM, keyPEM, caPEM)
}

func requestCert(ctx context.Context, cfg config.Config, csrPEM, caPEM []byte) ([]byte, error) {
	pool, err := pki.CAPool(caPEM)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13},
		},
	}

	reqBody, err := json.Marshal(enrollRequest{CSRPEM: string(csrPEM), AgentID: agentID()})
	if err != nil {
		return nil, fmt.Errorf("enroll: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.EnrollEndpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("enroll: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.EnrollToken)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("enroll: POST %s: %w", cfg.EnrollEndpoint, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("enroll: endpoint returned %s: %s", resp.Status, bytes.TrimSpace(body))
	}

	var er enrollResponse
	if err := json.Unmarshal(body, &er); err != nil {
		return nil, fmt.Errorf("enroll: decode response: %w", err)
	}
	if er.CertPEM == "" {
		return nil, fmt.Errorf("enroll: response contained no certificate")
	}
	return []byte(er.CertPEM), nil
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

func readIdentity(certPath, keyPath, caPath string) (cert, key, ca []byte, err error) {
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
