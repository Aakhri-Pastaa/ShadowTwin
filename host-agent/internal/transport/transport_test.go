package transport

import (
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/collectors"
	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/pki"
)

// mtlsServer starts an httptest server that requires and verifies client certs,
// and returns it, the CA that anchors it, and a ready client mTLS config.
func mtlsServer(t *testing.T, handler http.Handler) (*httptest.Server, *pki.CA, *tls.Config) {
	t.Helper()
	ca, err := pki.NewCA("test-ca")
	if err != nil {
		t.Fatalf("ca: %v", err)
	}
	serverPEM, serverKeyPEM, err := ca.IssueServerCert([]string{"127.0.0.1", "localhost"}, time.Hour)
	if err != nil {
		t.Fatalf("server cert: %v", err)
	}
	serverCert, err := tls.X509KeyPair(serverPEM, serverKeyPEM)
	if err != nil {
		t.Fatalf("server keypair: %v", err)
	}
	pool, err := pki.CAPool(ca.CertPEM)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}

	ts := httptest.NewUnstartedServer(handler)
	ts.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
	}
	ts.StartTLS()
	t.Cleanup(ts.Close)

	// Mint a client identity signed by the same CA.
	key, err := pki.GenerateKey()
	if err != nil {
		t.Fatalf("client key: %v", err)
	}
	keyPEM, err := pki.MarshalKeyPEM(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	csrPEM, err := pki.MarshalCSRPEM(key, "test-agent")
	if err != nil {
		t.Fatalf("csr: %v", err)
	}
	clientCertPEM, err := ca.SignCSR(csrPEM, time.Hour)
	if err != nil {
		t.Fatalf("sign client: %v", err)
	}
	clientTLS, err := pki.ClientConfig(clientCertPEM, keyPEM, ca.CertPEM)
	if err != nil {
		t.Fatalf("client config: %v", err)
	}
	return ts, ca, clientTLS
}

func status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}

func sampleEvents(n int) []collectors.Event {
	evs := make([]collectors.Event, n)
	for i := range evs {
		evs[i] = collectors.NewEvent("test", time.Now(), map[string]any{"i": i})
	}
	return evs
}

func TestSendSuccessGzipsAndCarriesEvents(t *testing.T) {
	var gotEncoding string
	var gotCount int
	h := func(w http.ResponseWriter, r *http.Request) {
		gotEncoding = r.Header.Get("Content-Encoding")
		var reader io.Reader = r.Body
		if gotEncoding == "gzip" {
			zr, err := gzip.NewReader(r.Body)
			if err != nil {
				http.Error(w, "bad gzip", http.StatusBadRequest)
				return
			}
			defer zr.Close()
			reader = zr
		}
		var env struct {
			Events []map[string]any `json:"events"`
		}
		if err := json.NewDecoder(reader).Decode(&env); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		gotCount = len(env.Events)
		w.WriteHeader(http.StatusAccepted)
	}
	ts, _, clientTLS := mtlsServer(t, http.HandlerFunc(h))
	s := New(ts.URL, "test-agent", clientTLS)

	if err := s.Send(context.Background(), sampleEvents(3)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotEncoding != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", gotEncoding)
	}
	if gotCount != 3 {
		t.Errorf("server received %d events, want 3", gotCount)
	}
}

func TestSendClassifiesResponses(t *testing.T) {
	cases := []struct {
		name string
		code int
		want error
	}{
		{"5xx", http.StatusInternalServerError, ErrRetryable},
		{"429", http.StatusTooManyRequests, ErrRetryable},
		{"408", http.StatusRequestTimeout, ErrRetryable},
		{"400", http.StatusBadRequest, ErrPermanent},
		{"403", http.StatusForbidden, ErrPermanent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts, _, clientTLS := mtlsServer(t, status(tc.code))
			s := New(ts.URL, "a", clientTLS)
			err := s.Send(context.Background(), sampleEvents(1))
			if !errors.Is(err, tc.want) {
				t.Fatalf("status %d: err = %v, want %v", tc.code, err, tc.want)
			}
		})
	}
}

func TestSendRequiresClientCert(t *testing.T) {
	ts, ca, _ := mtlsServer(t, status(http.StatusAccepted))
	pool, _ := pki.CAPool(ca.CertPEM)
	// Trusts the server, but presents no client cert.
	noCert := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13}
	s := New(ts.URL, "a", noCert)
	if err := s.Send(context.Background(), sampleEvents(1)); !errors.Is(err, ErrRetryable) {
		t.Fatalf("without client cert err = %v, want ErrRetryable", err)
	}
}

func TestSendRejectsUntrustedServer(t *testing.T) {
	ts, _, clientTLS := mtlsServer(t, status(http.StatusAccepted))
	// Pin a different CA: the server's cert is no longer trusted.
	otherCA, _ := pki.NewCA("other-ca")
	pool, _ := pki.CAPool(otherCA.CertPEM)
	clientTLS.RootCAs = pool
	s := New(ts.URL, "a", clientTLS)
	if err := s.Send(context.Background(), sampleEvents(1)); !errors.Is(err, ErrRetryable) {
		t.Fatalf("untrusted server err = %v, want ErrRetryable", err)
	}
}

func TestSendEmptyIsNoop(t *testing.T) {
	s := New("https://example.invalid", "a", &tls.Config{})
	if err := s.Send(context.Background(), nil); err != nil {
		t.Fatalf("empty Send err = %v, want nil", err)
	}
}

func TestBackoffStaysWithinCap(t *testing.T) {
	base := 100 * time.Millisecond
	max := time.Second
	for attempt := 1; attempt <= 25; attempt++ {
		d := Backoff(attempt, base, max)
		if d < 0 || d > max {
			t.Fatalf("Backoff(%d) = %v, want within [0,%v]", attempt, d, max)
		}
	}
}
