package auth

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/collectors"
)

// Fixtures shaped like real `journalctl -o json` lines.
const (
	fixtureSudoFail    = `{"__CURSOR":"s=aaa;i=0001;b=boot;m=1;t=1;x=1","__REALTIME_TIMESTAMP":"1718560000000000","PRIORITY":"5","SYSLOG_FACILITY":"10","SYSLOG_IDENTIFIER":"sudo","_COMM":"sudo","_PID":"4242","_UID":"1000","_SYSTEMD_UNIT":"session-3.scope","_HOSTNAME":"lab-host","_TRANSPORT":"syslog","MESSAGE":"pam_unix(sudo:auth): authentication failure; logname=kunal uid=1000 euid=0 tty=/dev/pts/0 ruser=kunal rhost= user=root"}`
	fixtureSSHDInvalid = `{"__CURSOR":"s=aaa;i=0002;b=boot;m=2;t=2;x=2","__REALTIME_TIMESTAMP":"1718560005000000","PRIORITY":"4","SYSLOG_FACILITY":"4","SYSLOG_IDENTIFIER":"sshd","_COMM":"sshd","_PID":"5050","_UID":"0","MESSAGE":"Invalid user attacker from 10.0.0.9 port 51514"}`
	fixtureLogin       = `{"__CURSOR":"s=aaa;i=0003;b=boot;m=3;t=3;x=3","__REALTIME_TIMESTAMP":"1718560009000000","PRIORITY":"6","SYSLOG_FACILITY":"10","SYSLOG_IDENTIFIER":"login","MESSAGE":"pam_unix(login:session): session opened for user kunal"}`
)

func TestParseEntry(t *testing.T) {
	tests := []struct {
		name        string
		line        string
		wantErr     bool
		wantCursor  string
		wantMicros  int64
		wantMessage string
	}{
		{
			name:        "well-formed sudo failure",
			line:        fixtureSudoFail,
			wantCursor:  "s=aaa;i=0001;b=boot;m=1;t=1;x=1",
			wantMicros:  1718560000000000,
			wantMessage: "pam_unix(sudo:auth): authentication failure; logname=kunal uid=1000 euid=0 tty=/dev/pts/0 ruser=kunal rhost= user=root",
		},
		{name: "invalid json", line: `{not valid json`, wantErr: true},
		{name: "missing cursor", line: `{"__REALTIME_TIMESTAMP":"1718560000000000","MESSAGE":"hi"}`, wantErr: true},
		{name: "missing timestamp", line: `{"__CURSOR":"s=aaa;i=9","MESSAGE":"hi"}`, wantErr: true},
		{name: "non-numeric timestamp", line: `{"__CURSOR":"s=aaa;i=9","__REALTIME_TIMESTAMP":"not-a-number"}`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, cursor, err := parseEntry([]byte(tt.line))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got none (event=%+v)", ev)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cursor != tt.wantCursor {
				t.Errorf("cursor = %q, want %q", cursor, tt.wantCursor)
			}
			if ev.Source != Name {
				t.Errorf("source = %q, want %q", ev.Source, Name)
			}
			if want := time.UnixMicro(tt.wantMicros).UTC(); !ev.Timestamp.Equal(want) {
				t.Errorf("timestamp = %v, want %v", ev.Timestamp, want)
			}
			if got, _ := ev.Payload["message"].(string); got != tt.wantMessage {
				t.Errorf("payload message = %q, want %q", got, tt.wantMessage)
			}
			if ev.ID == "" {
				t.Error("event ID is empty")
			}
			if ev.CollectedAt.IsZero() {
				t.Error("CollectedAt not set")
			}
		})
	}
}

var uuidV4Re = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// TestScanEmitsSkipsAndPersistsCursor drives scan with a fake journal reader
// (a strings.Reader) — no root, no live journald. It checks that malformed lines
// are skipped, that emitted IDs are unique v4 UUIDs, and that the last good
// cursor is persisted for resume.
func TestScanEmitsSkipsAndPersistsCursor(t *testing.T) {
	c := New(t.TempDir())

	input := strings.Join([]string{
		fixtureSudoFail,
		`{garbage`, // malformed: must be skipped, not fatal
		fixtureSSHDInvalid,
		fixtureLogin,
	}, "\n")

	out := make(chan collectors.Event, 8)
	if err := c.scan(context.Background(), strings.NewReader(input), out); err != nil {
		t.Fatalf("scan returned error: %v", err)
	}
	close(out)

	var events []collectors.Event
	for ev := range out {
		events = append(events, ev)
	}

	if len(events) != 3 {
		t.Fatalf("emitted %d events, want 3 (malformed line should be skipped)", len(events))
	}

	seen := make(map[string]bool)
	for i, ev := range events {
		if !uuidV4Re.MatchString(ev.ID) {
			t.Errorf("event %d ID %q is not a v4 UUID", i, ev.ID)
		}
		if seen[ev.ID] {
			t.Errorf("duplicate event ID %q", ev.ID)
		}
		seen[ev.ID] = true
	}

	got, err := c.readCursor()
	if err != nil {
		t.Fatalf("readCursor: %v", err)
	}
	if want := "s=aaa;i=0003;b=boot;m=3;t=3;x=3"; got != want {
		t.Errorf("persisted cursor = %q, want %q", got, want)
	}
}
