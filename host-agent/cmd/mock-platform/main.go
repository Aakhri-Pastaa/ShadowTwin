// Command mock-platform is a DEV-ONLY stand-in for the ShadowTwin platform's
// agent-facing endpoints (/enroll and /ingest). It exists to exercise the host
// agent's enrollment and mTLS shipping end to end until the real platform is
// built. It is not part of the shipped agent and must never be used in
// production. See docs/adr/0003-host-agent-telemetry-transport.md for the wire
// contract it implements.
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
	"time"

	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/pki"
)

func main() {
	addr := flag.String("addr", ":8443", "listen address")
	token := flag.String("token", "dev-enroll-token", "accepted enrollment bearer token")
	caOut := flag.String("ca-out", "mock-ca.crt", "path to write the CA cert (agent must trust this)")
	flag.Parse()

	ca, err := pki.NewCA("ShadowTwin Mock Platform CA")
	if err != nil {
		log.Fatalf("mock-platform: create CA: %v", err)
	}
	if err := os.WriteFile(*caOut, ca.CertPEM, 0o644); err != nil {
		log.Fatalf("mock-platform: write CA to %s: %v", *caOut, err)
	}

	serverCertPEM, serverKeyPEM, err := ca.IssueServerCert([]string{"localhost", "127.0.0.1"}, 24*time.Hour)
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

	mux := http.NewServeMux()
	mux.HandleFunc("/enroll", enrollHandler(ca, *token))
	mux.HandleFunc("/ingest", ingestHandler())

	srv := &http.Server{
		Addr:    *addr,
		Handler: mux,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{serverCert},
			ClientCAs:    clientCAs,
			// One listener serves both endpoints: /enroll has no client cert yet,
			// /ingest enforces one in its handler.
			ClientAuth: tls.VerifyClientCertIfGiven,
			MinVersion: tls.VersionTLS13,
		},
	}

	log.Printf("mock-platform listening on %s (DEV ONLY)", *addr)
	log.Printf("  CA written to %s — copy it to the agent's cert dir as ca.crt", *caOut)
	log.Printf("  enroll token: %s", *token)
	log.Printf("  set AGENT_ENROLL_ENDPOINT=https://localhost%s/enroll", *addr)
	log.Printf("  set INGEST_ENDPOINT=https://localhost%s/ingest", *addr)
	log.Fatal(srv.ListenAndServeTLS("", "")) // certs come from TLSConfig
}

func enrollHandler(ca *pki.CA, token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "invalid enroll token", http.StatusUnauthorized)
			return
		}
		var req struct {
			CSRPEM  string `json:"csr_pem"`
			AgentID string `json:"agent_id"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || req.CSRPEM == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		certPEM, err := ca.SignCSR([]byte(req.CSRPEM), 90*24*time.Hour)
		if err != nil {
			http.Error(w, "sign failed: "+err.Error(), http.StatusBadRequest)
			return
		}
		log.Printf("/enroll: issued certificate for agent_id=%q", req.AgentID)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"cert_pem": string(certPEM),
			"ca_pem":   string(ca.CertPEM),
		})
	}
}

func ingestHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			http.Error(w, "client certificate required", http.StatusUnauthorized)
			return
		}
		cn := r.TLS.PeerCertificates[0].Subject.CommonName

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
		log.Printf("/ingest: accepted %d event(s) from agent_id=%q (client CN=%q)",
			len(env.Events), env.AgentID, cn)
		for _, ev := range env.Events {
			log.Printf("    event id=%v source=%v", ev["id"], ev["source"])
		}
		w.WriteHeader(http.StatusAccepted)
	}
}
