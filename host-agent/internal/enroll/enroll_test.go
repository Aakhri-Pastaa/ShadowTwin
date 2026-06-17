package enroll

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/config"
	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/pki"
)

func TestEnsureIdentityEnrollsThenReuses(t *testing.T) {
	ca, err := pki.NewCA("test-ca")
	if err != nil {
		t.Fatalf("ca: %v", err)
	}
	srvCertPEM, srvKeyPEM, err := ca.IssueServerCert([]string{"127.0.0.1", "localhost"}, time.Hour)
	if err != nil {
		t.Fatalf("server cert: %v", err)
	}
	srvCert, err := tls.X509KeyPair(srvCertPEM, srvKeyPEM)
	if err != nil {
		t.Fatalf("server keypair: %v", err)
	}

	var hits int32
	var sawCSR, sawPrivateKey bool
	h := func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		var req struct {
			CSRPEM  string `json:"csr_pem"`
			AgentID string `json:"agent_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		sawCSR = strings.Contains(req.CSRPEM, "CERTIFICATE REQUEST")
		// The agent must never transmit its private key — only the CSR.
		sawPrivateKey = strings.Contains(req.CSRPEM, "PRIVATE KEY")
		certPEM, err := ca.SignCSR([]byte(req.CSRPEM), time.Hour)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"cert_pem": string(certPEM),
			"ca_pem":   string(ca.CertPEM),
		})
	}
	ts := httptest.NewUnstartedServer(http.HandlerFunc(h))
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{srvCert}, MinVersion: tls.VersionTLS13}
	ts.StartTLS()
	defer ts.Close()

	dir := t.TempDir()
	// The platform CA is provided out-of-band so the agent can trust the endpoint.
	if err := os.WriteFile(filepath.Join(dir, "ca.crt"), ca.CertPEM, 0o644); err != nil {
		t.Fatalf("seed ca.crt: %v", err)
	}
	cfg := config.Config{CertDir: dir, EnrollEndpoint: ts.URL, EnrollToken: "test-token"}

	tlsConf, err := EnsureIdentity(context.Background(), cfg)
	if err != nil {
		t.Fatalf("EnsureIdentity: %v", err)
	}
	if tlsConf == nil || len(tlsConf.Certificates) == 0 {
		t.Fatal("returned config has no client certificate")
	}
	if !sawCSR {
		t.Error("enroll endpoint did not receive a CSR")
	}
	if sawPrivateKey {
		t.Error("SECURITY: enrollment request contained a private key")
	}

	info, err := os.Stat(filepath.Join(dir, "client.key"))
	if err != nil {
		t.Fatalf("client.key: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("client.key mode = %o, want 600", perm)
	}

	// A second call must reuse the stored identity without re-enrolling.
	if _, err := EnsureIdentity(context.Background(), cfg); err != nil {
		t.Fatalf("reuse: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("enroll endpoint hit %d times, want 1 (second run should reuse)", got)
	}
}

func TestEnsureIdentityFailsWithoutToken(t *testing.T) {
	ca, _ := pki.NewCA("test-ca")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ca.crt"), ca.CertPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{CertDir: dir, EnrollEndpoint: "https://localhost:1/enroll", EnrollToken: ""}
	if _, err := EnsureIdentity(context.Background(), cfg); err == nil {
		t.Fatal("expected error with no client cert and no enroll token")
	}
}

func TestEnsureIdentityFailsWithoutCA(t *testing.T) {
	dir := t.TempDir() // no ca.crt present
	cfg := config.Config{CertDir: dir, EnrollEndpoint: "https://localhost:1/enroll", EnrollToken: "x"}
	if _, err := EnsureIdentity(context.Background(), cfg); err == nil {
		t.Fatal("expected error when the platform CA is absent")
	}
}
