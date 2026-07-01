// Package auth collects authentication events from systemd-journald.
//
// Rather than linking libsystemd through cgo, it drives `journalctl -o json` as a
// subprocess and normalizes each entry into a collectors.Event. That keeps the
// agent a single static binary that cross-compiles trivially (see docs/adr/0002):
// journalctl is the proven tool we drive, the same pattern other collectors will
// use for osquery and auditd. Resumption uses journald's cursor (the __CURSOR
// field plus --after-cursor), never naive file tailing, so a clean restart
// resumes exactly where it left off.
//
// Reading auth/authpriv events needs journal read access, not root: membership in
// the systemd-journal group (or adm/wheel on Debian/Ubuntu, which systemd's ACLs
// grant). The eventual least-privilege target is a dedicated unprivileged service
// user in that group plus unit hardening — not a capability on this binary.
// (CAP_AUDIT_READ governs the kernel audit socket, which would matter for a
// future auditd collector, not this journald one.)
package auth

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/collectors"
)

const (
	// Name is the collector's stable identifier, used as Event.Source.
	Name = "auth.journald"

	cursorFileName = "auth.journald.cursor"

	// processStopGrace is how long journalctl gets to exit after SIGTERM on
	// shutdown before the context force-kills it.
	processStopGrace = 5 * time.Second

	// maxLineBytes caps a single journal line; MESSAGE fields can be large.
	maxLineBytes = 1 << 20
)

// authFacilities are the syslog facilities we follow: auth (4) and authpriv
// (10). Together these carry sshd, sudo, su, login, PAM, and polkit activity —
// the classic auth.log set.
var authFacilities = []string{"4", "10"}

// Collector reads authentication events from systemd-journald via journalctl. It
// satisfies collectors.Collector.
type Collector struct {
	cursorPath string

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

var _ collectors.Collector = (*Collector)(nil)

// New returns an auth collector that persists its resume cursor under stateDir.
func New(stateDir string) *Collector {
	return &Collector{cursorPath: filepath.Join(stateDir, cursorFileName)}
}

// Name returns the collector's stable identifier.
func (c *Collector) Name() string { return Name }

// Start launches journalctl and returns a channel of auth events. It is
// non-blocking; reading and parsing run in a background goroutine. A non-nil
// error means the collector could not start (journalctl missing, or the cursor
// state file is unreadable).
func (c *Collector) Start(ctx context.Context) (<-chan collectors.Event, error) {
	journalctl, err := exec.LookPath("journalctl")
	if err != nil {
		return nil, fmt.Errorf("journalctl not found in PATH: %w", err)
	}

	cursor, err := c.readCursor()
	if err != nil {
		return nil, fmt.Errorf("reading resume cursor: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)

	cmd := exec.CommandContext(ctx, journalctl, journalctlArgs(cursor)...)
	// Prefer a graceful SIGTERM on shutdown; the context force-kills the process
	// if it has not exited within the grace period.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = processStopGrace
	cmd.Stderr = os.Stderr // surface journalctl's own diagnostics on our stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("connecting journalctl stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("starting journalctl: %w", err)
	}

	if cursor == "" {
		log.Printf("auth: following journald from now (no saved cursor)")
	} else {
		log.Printf("auth: resuming journald after saved cursor")
	}

	out := make(chan collectors.Event)

	c.mu.Lock()
	c.cancel = cancel
	c.done = make(chan struct{})
	c.mu.Unlock()

	go c.run(ctx, cmd, stdout, out)
	return out, nil
}

// Stop signals shutdown and blocks until the collector has fully exited and the
// final cursor is persisted. It is idempotent and safe after ctx cancellation.
func (c *Collector) Stop() error {
	c.mu.Lock()
	cancel, done := c.cancel, c.done
	c.mu.Unlock()

	if cancel == nil {
		return nil // never started
	}
	cancel()
	<-done

	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Collector) run(ctx context.Context, cmd *exec.Cmd, stdout io.ReadCloser, out chan<- collectors.Event) {
	// close(out) is registered last so it runs first (LIFO): the channel is
	// closed before done is signalled, so a caller waking from Stop() sees a
	// drained, closed channel.
	defer close(c.done)
	defer close(out)

	scanErr := c.scan(ctx, stdout, out)
	waitErr := cmd.Wait()

	// A cancelled context means we asked journalctl to stop (signal or Stop());
	// the resulting scan EOF and signal-kill in waitErr are a clean shutdown.
	if ctx.Err() != nil {
		return
	}
	switch {
	case scanErr != nil:
		c.storeErr(fmt.Errorf("reading journal: %w", scanErr))
	case waitErr != nil:
		c.storeErr(fmt.Errorf("journalctl exited unexpectedly: %w", waitErr))
	}
}

// scan reads journalctl's newline-delimited JSON, emits an Event per valid
// entry, and persists the cursor after each emit. Malformed lines are logged to
// stderr and skipped — a single bad entry must never take the collector down.
func (c *Collector) scan(ctx context.Context, r io.Reader, out chan<- collectors.Event) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), maxLineBytes)

	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		ev, cursor, err := parseEntry(line)
		if err != nil {
			log.Printf("auth: skipping malformed journal entry: %v", err)
			continue
		}
		select {
		case out <- ev:
		case <-ctx.Done():
			return ctx.Err()
		}
		// Persist after emit: on a crash between the two we re-read this entry on
		// restart (at-least-once) rather than lose it. Cross-restart dedup arrives
		// with the buffer/shipper slice.
		if err := c.writeCursor(cursor); err != nil {
			log.Printf("auth: failed to persist cursor: %v", err)
		}
	}
	return sc.Err()
}

func (c *Collector) storeErr(err error) {
	log.Printf("auth: %v", err)
	c.mu.Lock()
	c.err = err
	c.mu.Unlock()
}

// journalctlArgs builds the journalctl invocation. With no saved cursor we follow
// from now (--lines=0) to avoid replaying the whole journal on first run;
// otherwise we resume after the saved cursor.
func journalctlArgs(cursor string) []string {
	args := []string{"--output=json", "--follow"}
	if cursor == "" {
		args = append(args, "--lines=0")
	} else {
		args = append(args, "--after-cursor="+cursor)
	}
	for _, f := range authFacilities {
		args = append(args, "SYSLOG_FACILITY="+f)
	}
	return args
}

func (c *Collector) readCursor() (string, error) {
	b, err := os.ReadFile(c.cursorPath)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil // first run
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// writeCursor persists the cursor atomically (write temp + rename) so a crash
// mid-write can't leave a truncated cursor file.
func (c *Collector) writeCursor(cursor string) error {
	tmp := c.cursorPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(cursor+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.cursorPath)
}
