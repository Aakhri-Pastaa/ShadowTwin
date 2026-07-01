// Command mock-platform is a DEV-ONLY stand-in for the ShadowTwin platform's
// agent-facing endpoints (/ca, /enroll, /renew, /ingest, /revoke). It exists to
// exercise the host agent's onboarding, mTLS shipping, certificate renewal, and
// revocation end to end until the real platform is built. It is not part of the
// shipped agent and must never be used in production. See
// docs/adr/0003 and docs/adr/0004 for the wire contract it implements.
package main

import (
	"compress/gzip"
	"crypto/tls"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/pki"
)

const certTTL = 90 * 24 * time.Hour

type server struct {
	ca    *pki.CA
	token string

	mu      sync.RWMutex
	revoked map[string]bool // revoked agent IDs (client-cert CommonName)
}

func main() {
	addr := flag.String("addr", ":8443", "listen address")
	token := flag.String("token", "dev-enroll-token", "accepted enrollment/admin bearer token")
	caOut := flag.String("ca-out", "mock-ca.crt", "path to write the CA cert (agents must trust this)")
	host := flag.String("host", "", "extra DNS name(s)/IP(s) for the server cert SAN, comma-separated (for connecting over a network instead of localhost)")
	flag.Parse()

	ca, err := pki.NewCA("ShadowTwin Mock Platform CA")
	if err != nil {
		log.Fatalf("mock-platform: create CA: %v", err)
	}
	if err := os.WriteFile(*caOut, ca.CertPEM, 0o644); err != nil {
		log.Fatalf("mock-platform: write CA to %s: %v", *caOut, err)
	}

	hosts := []string{"localhost", "127.0.0.1"}
	for _, h := range strings.Split(*host, ",") {
		if h = strings.TrimSpace(h); h != "" {
			hosts = append(hosts, h)
		}
	}
	serverCertPEM, serverKeyPEM, err := ca.IssueServerCert(hosts, 24*time.Hour)
	if err != nil {
		log.Fatalf("mock-platform: issue server cert: %v", err)
	}
	serverCert, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	if err != nil {
		log.Fatalf("mock-platform: load server keypair: %v", err)
	}
	clientCAs, err := pki.CAPool(ca.CertPEM)
	if err != nil {
		log.Fatalf("mock-platform: client CA pool: %v", err)
	}

	s := &server{ca: ca, token: *token, revoked: map[string]bool{}}

	mux := http.NewServeMux()
	mux.HandleFunc("/ca", s.handleCA)         // non-secret: hand out the CA
	mux.HandleFunc("/enroll", s.handleEnroll) // token-authenticated
	mux.HandleFunc("/renew", s.handleRenew)   // mTLS-authenticated
	mux.HandleFunc("/ingest", s.handleIngest) // mTLS-authenticated
	mux.HandleFunc("/revoke", s.handleRevoke) // admin (token): deny an agent

	srv := &http.Server{
		Addr:    *addr,
		Handler: mux,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{serverCert},
			ClientCAs:    clientCAs,
			// /ca and /enroll have no client cert yet; /renew and /ingest enforce
			// one in their handlers.
			ClientAuth: tls.VerifyClientCertIfGiven,
			MinVersion: tls.VersionTLS13,
		},
	}

	log.Printf("mock-platform listening on %s (DEV ONLY)", *addr)
	log.Printf("  CA written to %s — agents must trust it (ca.crt)", *caOut)
	log.Printf("  server cert SANs: %s", strings.Join(hosts, ", "))
	log.Printf("  enroll/admin token: %s", *token)
	log.Fatal(srv.ListenAndServeTLS("", "")) // certs come from TLSConfig
}

// handleCA serves the CA certificate. It is non-secret (it only certifies; it
// can't impersonate), so onboarding can fetch it with `--ca <url>`.
func (s *server) handleCA(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/x-pem-file")
	_, _ = w.Write(s.ca.CertPEM)
}

// handleEnroll issues a first certificate to an agent presenting the bearer
// token (the one-time bootstrap secret).
func (s *server) handleEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+s.token {
		http.Error(w, "invalid enroll token", http.StatusUnauthorized)
		return
	}
	req, ok := decodeCSR(w, r)
	if !ok {
		return
	}
	s.issue(w, req.CSRPEM, "enroll", req.AgentID)
}

// handleRenew issues a fresh certificate to an already-enrolled agent,
// authenticated by its current client certificate (mTLS) — no token needed.
func (s *server) handleRenew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	cn, ok := clientCN(r)
	if !ok {
		http.Error(w, "client certificate required", http.StatusUnauthorized)
		return
	}
	if s.isRevoked(cn) {
		log.Printf("/renew: REFUSED revoked agent %q", cn)
		http.Error(w, "agent revoked", http.StatusForbidden)
		return
	}
	req, ok := decodeCSR(w, r)
	if !ok {
		return
	}
	s.issue(w, req.CSRPEM, "renew", cn)
}

// handleIngest accepts a batch from an enrolled (and not revoked) agent.
func (s *server) handleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	cn, ok := clientCN(r)
	if !ok {
		http.Error(w, "client certificate required", http.StatusUnauthorized)
		return
	}
	if s.isRevoked(cn) {
		log.Printf("/ingest: REFUSED revoked agent %q", cn)
		http.Error(w, "agent revoked", http.StatusForbidden)
		return
	}

	var reader io.Reader = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			http.Error(w, "bad gzip", http.StatusBadRequest)
			return
		}
		defer zr.Close()
		reader = zr
	}
	var env struct {
		AgentID string           `json:"agent_id"`
		Events  []map[string]any `json:"events"`
	}
	if err := json.NewDecoder(reader).Decode(&env); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	log.Printf("/ingest: accepted %d event(s) from agent_id=%q (client CN=%q)", len(env.Events), env.AgentID, cn)
	for _, ev := range env.Events {
		log.Printf("    event id=%v source=%v", ev["id"], ev["source"])
	}
	w.WriteHeader(http.StatusAccepted)
}

// handleRevoke is the admin action that denies an agent. Real platforms do this
// from their console; here it's a token-protected endpoint for demoing.
func (s *server) handleRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+s.token {
		http.Error(w, "invalid admin token", http.StatusUnauthorized)
		return
	}
	var req struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil || req.AgentID == "" {
		http.Error(w, "bad request (need agent_id)", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.revoked[req.AgentID] = true
	s.mu.Unlock()
	log.Printf("/revoke: agent %q is now revoked", req.AgentID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) issue(w http.ResponseWriter, csrPEM, kind, who string) {
	certPEM, err := s.ca.SignCSR([]byte(csrPEM), certTTL)
	if err != nil {
		http.Error(w, "sign failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("/%s: issued certificate for agent_id=%q", kind, who)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"cert_pem": string(certPEM),
		"ca_pem":   string(s.ca.CertPEM),
	})
}

func (s *server) isRevoked(cn string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revoked[cn]
}

type csrBody struct {
	CSRPEM  string `json:"csr_pem"`
	AgentID string `json:"agent_id"`
}

func decodeCSR(w http.ResponseWriter, r *http.Request) (csrBody, bool) {
	var req csrBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || req.CSRPEM == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return csrBody{}, false
	}
	return req, true
}

func clientCN(r *http.Request) (string, bool) {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return "", false
	}
	return r.TLS.PeerCertificates[0].Subject.CommonName, true
}
