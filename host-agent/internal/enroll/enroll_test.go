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

// testPlatform mimics the mock platform's /enroll (token) and /renew (mTLS)
// endpoints, signing CSRs with the given CA. It records how often each is hit.
func testPlatform(t *testing.T, ca *pki.CA, token string, enrollHits, renewHits *int32) *httptest.Server {
	t.Helper()
	srvCertPEM, srvKeyPEM, err := ca.IssueServerCert([]string{"127.0.0.1", "localhost"}, time.Hour)
	if err != nil {
		t.Fatalf("server cert: %v", err)
	}
	srvCert, err := tls.X509KeyPair(srvCertPEM, srvKeyPEM)
	if err != nil {
		t.Fatalf("server keypair: %v", err)
	}
	pool, _ := pki.CAPool(ca.CertPEM)

	sign := func(w http.ResponseWriter, r *http.Request) bool {
		var req struct {
			CSRPEM string `json:"csr_pem"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CSRPEM == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return false
		}
		// Security: the request must carry a CSR, never the private key.
		if strings.Contains(req.CSRPEM, "PRIVATE KEY") {
			t.Errorf("SECURITY: request body contained a private key")
		}
		certPEM, err := ca.SignCSR([]byte(req.CSRPEM), time.Hour)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return false
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"cert_pem": string(certPEM), "ca_pem": string(ca.CertPEM)})
		return true
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/enroll", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(enrollHits, 1)
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		sign(w, r)
	})
	mux.HandleFunc("/renew", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(renewHits, 1)
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			http.Error(w, "client certificate required", http.StatusUnauthorized)
			return
		}
		sign(w, r)
	})

	ts := httptest.NewUnstartedServer(mux)
	ts.TLS = &tls.Config{
		Certificates: []tls.Certificate{srvCert},
		ClientCAs:    pool,
		ClientAuth:   tls.VerifyClientCertIfGiven,
		MinVersion:   tls.VersionTLS13,
	}
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return ts
}

func writeCA(t *testing.T, dir string, ca *pki.CA) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "ca.crt"), ca.CertPEM, 0o644); err != nil {
		t.Fatalf("seed ca.crt: %v", err)
	}
}

func testConfig(dir, base string) config.Config {
	return config.Config{
		CertDir:        dir,
		EnrollEndpoint: base + "/enroll",
		RenewEndpoint:  base + "/renew",
		EnrollToken:    "tok",
	}
}

func TestEnsureIdentityEnrollsThenReuses(t *testing.T) {
	ca, _ := pki.NewCA("test-ca")
	var enrollHits, renewHits int32
	ts := testPlatform(t, ca, "tok", &enrollHits, &renewHits)

	dir := t.TempDir()
	writeCA(t, dir, ca)
	cfg := testConfig(dir, ts.URL)

	id, err := EnsureIdentity(context.Background(), cfg)
	if err != nil {
		t.Fatalf("EnsureIdentity: %v", err)
	}
	if !time.Now().Before(id.NotAfter()) {
		t.Fatal("issued certificate is already expired")
	}
	cert, err := id.TLSConfig().GetClientCertificate(&tls.CertificateRequestInfo{})
	if err != nil || cert == nil || len(cert.Certificate) == 0 {
		t.Fatalf("config presents no client certificate: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "client.key"))
	if err != nil {
		t.Fatalf("client.key: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("client.key mode = %o, want 600", perm)
	}

	// Second call reuses the stored identity — no new enrollment.
	if _, err := EnsureIdentity(context.Background(), cfg); err != nil {
		t.Fatalf("reuse: %v", err)
	}
	if got := atomic.LoadInt32(&enrollHits); got != 1 {
		t.Errorf("enroll endpoint hit %d times, want 1 (second run should reuse)", got)
	}
}

func TestRenewReissuesCertificate(t *testing.T) {
	ca, _ := pki.NewCA("test-ca")
	var enrollHits, renewHits int32
	ts := testPlatform(t, ca, "tok", &enrollHits, &renewHits)
	dir := t.TempDir()
	writeCA(t, dir, ca)
	cfg := testConfig(dir, ts.URL)

	id, err := EnsureIdentity(context.Background(), cfg)
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	id.mu.RLock()
	before := id.leaf.SerialNumber.String()
	id.mu.RUnlock()

	if err := id.Renew(context.Background(), cfg); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if got := atomic.LoadInt32(&renewHits); got != 1 {
		t.Errorf("renew endpoint hit %d times, want 1", got)
	}
	id.mu.RLock()
	after := id.leaf.SerialNumber.String()
	id.mu.RUnlock()
	if before == after {
		t.Error("renewal did not change the certificate (same serial)")
	}
	if cert, err := id.TLSConfig().GetClientCertificate(&tls.CertificateRequestInfo{}); err != nil || cert == nil {
		t.Fatalf("config presents no client certificate after renewal: %v", err)
	}
}

func TestNeedsRenewal(t *testing.T) {
	ca, _ := pki.NewCA("test-ca")
	var e, r int32
	ts := testPlatform(t, ca, "tok", &e, &r)
	dir := t.TempDir()
	writeCA(t, dir, ca)

	id, err := EnsureIdentity(context.Background(), testConfig(dir, ts.URL))
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	// The test platform issues 1-hour certs.
	if id.NeedsRenewal(time.Minute) {
		t.Error("a ~1h cert should not need renewal within a 1-minute window")
	}
	if !id.NeedsRenewal(48 * time.Hour) {
		t.Error("a ~1h cert should need renewal within a 48-hour window")
	}
}

func TestEnsureIdentityFailsWithoutToken(t *testing.T) {
	ca, _ := pki.NewCA("test-ca")
	dir := t.TempDir()
	writeCA(t, dir, ca)
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
