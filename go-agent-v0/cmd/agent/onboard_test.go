package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/pki"
)

func TestEndpointsFromServer(t *testing.T) {
	for _, base := range []string{"https://host:8443", "https://host:8443/"} {
		e, i, r, c := endpointsFromServer(base)
		if e != "https://host:8443/enroll" || i != "https://host:8443/ingest" ||
			r != "https://host:8443/renew" || c != "https://host:8443/ca" {
			t.Errorf("endpointsFromServer(%q) = %q %q %q %q", base, e, i, r, c)
		}
	}
}

func TestRenderUnitHardening(t *testing.T) {
	o := installOpts{
		user:     "shadowtwin",
		binPath:  "/usr/local/bin/shadowtwin-agent",
		stateDir: "/var/lib/shadowtwin-agent",
		envFile:  "/etc/shadowtwin-agent/agent.env",
		svcName:  "shadowtwin-agent",
	}
	unit := renderUnit(o)
	for _, want := range []string{
		"User=shadowtwin",
		"Group=shadowtwin",
		"ExecStart=/usr/local/bin/shadowtwin-agent",
		"EnvironmentFile=/etc/shadowtwin-agent/agent.env",
		"NoNewPrivileges=true",
		"ProtectSystem=strict",
		"CapabilityBoundingSet=",
		"ReadWritePaths=/var/lib/shadowtwin-agent",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit missing %q\n---\n%s", want, unit)
		}
	}
}

func TestEnvFileContent(t *testing.T) {
	o := installOpts{stateDir: "/var/lib/shadowtwin-agent", token: "secret-token"}
	got := envFileContent(o, "https://h/enroll", "https://h/ingest", "https://h/renew")
	for _, want := range []string{
		"HOST_AGENT_STATE_DIR=/var/lib/shadowtwin-agent",
		"INGEST_ENDPOINT=https://h/ingest",
		"AGENT_ENROLL_ENDPOINT=https://h/enroll",
		"AGENT_RENEW_ENDPOINT=https://h/renew",
		"AGENT_ENROLL_TOKEN=secret-token",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("env file missing %q\n---\n%s", want, got)
		}
	}
	if !strings.HasPrefix(got, "#") {
		t.Error("env file should start with a comment line")
	}
}

func TestParseEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.env")
	content := "# a comment\n\nINGEST_ENDPOINT=https://h/ingest\n  AGENT_ENROLL_TOKEN = tok \n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := parseEnvFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m["INGEST_ENDPOINT"] != "https://h/ingest" {
		t.Errorf("INGEST_ENDPOINT = %q", m["INGEST_ENDPOINT"])
	}
	if m["AGENT_ENROLL_TOKEN"] != "tok" {
		t.Errorf("AGENT_ENROLL_TOKEN = %q (want trimmed 'tok')", m["AGENT_ENROLL_TOKEN"])
	}
}

func TestFetchCAInsecure(t *testing.T) {
	ca, err := pki.NewCA("test-ca")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(ca.CertPEM)
	}))
	defer ts.Close()

	got, err := fetchCAInsecure(ts.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if string(got) != string(ca.CertPEM) {
		t.Error("fetched CA does not match served CA")
	}
}

func TestCertNotAfter(t *testing.T) {
	ca, err := pki.NewCA("test-ca")
	if err != nil {
		t.Fatal(err)
	}
	certPEM, _, err := ca.IssueServerCert([]string{"localhost"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "c.crt")
	if err := os.WriteFile(path, certPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	na, err := certNotAfter(path)
	if err != nil {
		t.Fatalf("certNotAfter: %v", err)
	}
	if d := time.Until(na); d < 55*time.Minute || d > 65*time.Minute {
		t.Errorf("NotAfter in %s, want ~1h", d)
	}
}
