package buffer

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/collectors"
)

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// makeEvent builds an event with a deterministic, strictly increasing
// CollectedAt so the queue's FIFO ordering is testable.
func makeEvent(seq int) collectors.Event {
	ev := collectors.NewEvent("test.source",
		base.Add(time.Duration(seq)*time.Second),
		map[string]any{"seq": seq})
	ev.CollectedAt = base.Add(time.Duration(seq) * time.Millisecond)
	return ev
}

func TestWriteReadAckFIFO(t *testing.T) {
	q, err := Open(t.TempDir(), Limits{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	var ids []string
	for i := 0; i < 3; i++ {
		ev := makeEvent(i)
		ids = append(ids, ev.ID)
		if err := q.Write(ev); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if got := q.Len(); got != 3 {
		t.Fatalf("Len = %d, want 3", got)
	}

	batch, err := q.ReadBatch(10, 0)
	if err != nil {
		t.Fatalf("read batch: %v", err)
	}
	if batch.Len() != 3 {
		t.Fatalf("batch len = %d, want 3", batch.Len())
	}
	for i, ev := range batch.Events {
		if ev.ID != ids[i] {
			t.Errorf("event %d: ID = %s, want %s (FIFO order broken)", i, ev.ID, ids[i])
		}
	}

	if err := q.Ack(batch); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if got := q.Len(); got != 0 {
		t.Fatalf("after ack Len = %d, want 0", got)
	}
}

func TestReadBatchRespectsMaxN(t *testing.T) {
	q, _ := Open(t.TempDir(), Limits{})
	for i := 0; i < 5; i++ {
		if err := q.Write(makeEvent(i)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	drained := 0
	for {
		b, err := q.ReadBatch(2, 0)
		if err != nil {
			t.Fatalf("read batch: %v", err)
		}
		if b.Len() == 0 {
			break
		}
		if b.Len() > 2 {
			t.Fatalf("batch len = %d, want <= 2", b.Len())
		}
		drained += b.Len()
		if err := q.Ack(b); err != nil {
			t.Fatalf("ack: %v", err)
		}
	}
	if drained != 5 {
		t.Fatalf("drained %d, want 5", drained)
	}
}

func TestReadBatchRespectsMaxBytes(t *testing.T) {
	q, _ := Open(t.TempDir(), Limits{})
	for i := 0; i < 3; i++ {
		if err := q.Write(makeEvent(i)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	// A 1-byte cap can never fit two events, but ReadBatch always returns at
	// least one so the queue can't stall.
	for i := 0; i < 3; i++ {
		b, err := q.ReadBatch(10, 1)
		if err != nil {
			t.Fatalf("read batch: %v", err)
		}
		if b.Len() != 1 {
			t.Fatalf("with 1-byte cap got %d events, want 1", b.Len())
		}
		if err := q.Ack(b); err != nil {
			t.Fatalf("ack: %v", err)
		}
	}
	if q.Len() != 0 {
		t.Fatalf("Len = %d, want 0", q.Len())
	}
}

func TestReopenRecoversPending(t *testing.T) {
	dir := t.TempDir()
	q, _ := Open(dir, Limits{})

	var ids []string
	for i := 0; i < 3; i++ {
		ev := makeEvent(i)
		ids = append(ids, ev.ID)
		if err := q.Write(ev); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	// Simulate a restart: a fresh queue over the same directory must see the
	// events that were never acked.
	q2, err := Open(dir, Limits{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := q2.Len(); got != 3 {
		t.Fatalf("after reopen Len = %d, want 3", got)
	}
	b, err := q2.ReadBatch(10, 0)
	if err != nil {
		t.Fatalf("read batch: %v", err)
	}
	for i, ev := range b.Events {
		if ev.ID != ids[i] {
			t.Errorf("reopened event %d: ID = %s, want %s", i, ev.ID, ids[i])
		}
	}
}

func TestDeadLetter(t *testing.T) {
	dir := t.TempDir()
	q, _ := Open(dir, Limits{})
	for i := 0; i < 2; i++ {
		if err := q.Write(makeEvent(i)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	b, _ := q.ReadBatch(10, 0)
	if err := q.DeadLetter(b); err != nil {
		t.Fatalf("dead-letter: %v", err)
	}
	if q.Len() != 0 {
		t.Fatalf("Len = %d, want 0", q.Len())
	}

	entries, err := os.ReadDir(filepath.Join(dir, deadDirName))
	if err != nil {
		t.Fatalf("read dead dir: %v", err)
	}
	got := 0
	for _, e := range entries {
		if !e.IsDir() {
			got++
		}
	}
	if got != 2 {
		t.Fatalf("dead dir has %d files, want 2", got)
	}
}

func TestBoundDropsOldest(t *testing.T) {
	q, _ := Open(t.TempDir(), Limits{MaxEvents: 3})

	var ids []string
	for i := 0; i < 5; i++ {
		ev := makeEvent(i)
		ids = append(ids, ev.ID)
		if err := q.Write(ev); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	if got := q.Len(); got != 3 {
		t.Fatalf("Len = %d, want 3 (bounded)", got)
	}
	if got := q.Dropped(); got != 2 {
		t.Fatalf("Dropped = %d, want 2", got)
	}

	b, _ := q.ReadBatch(10, 0)
	want := ids[2:] // the three newest survive; the two oldest were dropped
	if b.Len() != len(want) {
		t.Fatalf("kept %d events, want %d", b.Len(), len(want))
	}
	for i, ev := range b.Events {
		if ev.ID != want[i] {
			t.Errorf("kept event %d: ID = %s, want %s", i, ev.ID, want[i])
		}
	}
}

func TestCorruptQueuedFileDeadLettered(t *testing.T) {
	dir := t.TempDir()
	q, _ := Open(dir, Limits{})

	good := makeEvent(5)
	if err := q.Write(good); err != nil {
		t.Fatalf("write: %v", err)
	}
	// A garbage file sorts before the good event (smaller numeric prefix) and
	// must not wedge the queue: ReadBatch skips it into the dead-letter dir.
	const garbageName = "00000000000000000001-garbage.json"
	if err := os.WriteFile(filepath.Join(dir, garbageName), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seed garbage: %v", err)
	}

	b, err := q.ReadBatch(10, 0)
	if err != nil {
		t.Fatalf("read batch: %v", err)
	}
	if b.Len() != 1 || b.Events[0].ID != good.ID {
		t.Fatalf("expected only the good event, got %d", b.Len())
	}
	if _, err := os.Stat(filepath.Join(dir, deadDirName, garbageName)); err != nil {
		t.Fatalf("garbage not dead-lettered: %v", err)
	}
}

func TestEventFilesArePrivate(t *testing.T) {
	dir := t.TempDir()
	q, _ := Open(dir, Limits{})
	if err := q.Write(makeEvent(0)); err != nil {
		t.Fatalf("write: %v", err)
	}

	entries, _ := os.ReadDir(dir)
	checked := false
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), fileSuffix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			t.Errorf("event file %s mode = %o, want no group/other access", e.Name(), perm)
		}
		checked = true
	}
	if !checked {
		t.Fatal("no event file found")
	}
}

func TestConcurrentWriteAndDrain(t *testing.T) {
	q, _ := Open(t.TempDir(), Limits{})
	const n = 200

	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		for i := 0; i < n; i++ {
			if err := q.Write(makeEvent(i)); err != nil {
				t.Errorf("write %d: %v", i, err) // Errorf is safe from a goroutine
				return
			}
		}
	}()

	drained := 0
	for drained < n {
		b, err := q.ReadBatch(50, 0)
		if err != nil {
			t.Fatalf("read batch: %v", err)
		}
		if b.Len() == 0 {
			runtime.Gosched()
			continue
		}
		drained += b.Len()
		if err := q.Ack(b); err != nil {
			t.Fatalf("ack: %v", err)
		}
	}
	<-writeDone
}
