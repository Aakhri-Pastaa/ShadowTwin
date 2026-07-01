// Package buffer is the host agent's disk-backed event queue. It decouples
// collection from delivery: collectors write events here, and a sink (stdout
// today, the mTLS shipper in the next slice) drains them. Because every event is
// persisted before it is shipped, the agent rides out a process crash or a
// platform/network outage without dropping telemetry.
//
// The queue is a Maildir-style spool: one event per file, written atomically
// (temp file + rename, mode 0600) and named so a lexical sort is FIFO. This
// reuses the atomic-write discipline already used for the journald cursor, keeps
// the format trivially auditable, and adds zero dependencies. Delivery is
// at-least-once: an event is removed only after the sink acknowledges it, and
// every event carries a UUID so the platform can dedupe re-sends.
package buffer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/collectors"
)

const (
	dirPerm     = 0o700
	filePerm    = 0o600
	fileSuffix  = ".json"
	tmpSuffix   = ".tmp"
	deadDirName = "dead"
)

// Limits bounds the on-disk queue so a stuck sink or a runaway collector cannot
// exhaust the disk. A zero value for a dimension means "no limit" for it.
type Limits struct {
	MaxEvents int   // maximum pending event files; 0 = unlimited
	MaxBytes  int64 // maximum total bytes of pending events; 0 = unlimited
}

// Queue is a crash-safe, FIFO, on-disk queue of events. It is safe for
// concurrent use; in practice one producer writes and one consumer drains.
type Queue struct {
	dir     string // pending events live here
	deadDir string // poison events the sink permanently rejected
	limits  Limits

	mu      sync.Mutex
	count   int   // pending event files (kept in memory to bound cheaply)
	bytes   int64 // total bytes of pending events
	dropped int   // events discarded because the queue was full
}

// Batch is a set of events read from the queue together with the files backing
// them, so the caller can Ack (delete) or DeadLetter them after a ship attempt.
type Batch struct {
	Events []collectors.Event
	files  []string // absolute paths, parallel to Events
}

// Len reports how many events the batch holds.
func (b Batch) Len() int { return len(b.Events) }

// Open prepares the queue rooted at dir (creating it and its dead-letter
// subdirectory), clears orphaned temp files left by a crash mid-write, and
// counts any events already on disk so they are drained on this run.
func Open(dir string, lim Limits) (*Queue, error) {
	deadDir := filepath.Join(dir, deadDirName)
	if err := os.MkdirAll(deadDir, dirPerm); err != nil {
		return nil, fmt.Errorf("buffer: create queue dir %q: %w", dir, err)
	}
	q := &Queue{dir: dir, deadDir: deadDir, limits: lim}

	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.initLocked(); err != nil {
		return nil, err
	}
	return q, nil
}

func (q *Queue) initLocked() error {
	entries, err := os.ReadDir(q.dir)
	if err != nil {
		return fmt.Errorf("buffer: scan queue: %w", err)
	}
	var count int
	var bytes int64
	for _, e := range entries {
		name := e.Name()
		switch {
		case e.IsDir():
			continue
		case strings.HasSuffix(name, tmpSuffix):
			_ = os.Remove(filepath.Join(q.dir, name)) // orphaned partial write
		case strings.HasSuffix(name, fileSuffix):
			info, err := e.Info()
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return fmt.Errorf("buffer: stat %s: %w", name, err)
			}
			count++
			bytes += info.Size()
		}
	}
	q.count, q.bytes = count, bytes
	return nil
}

// Write persists one event to the queue. If the queue is at its configured
// limit, the oldest events are dropped first (with a warning) to make room, so a
// stalled sink degrades to losing the oldest telemetry rather than the host's
// disk.
func (q *Queue) Write(ev collectors.Event) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("buffer: marshal event %s: %w", ev.ID, err)
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if err := q.enforceLimitsLocked(int64(len(data))); err != nil {
		return err
	}

	final := filepath.Join(q.dir, q.fileName(ev))
	tmp := final + tmpSuffix
	if err := os.WriteFile(tmp, data, filePerm); err != nil {
		return fmt.Errorf("buffer: write temp file: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("buffer: commit event file: %w", err)
	}
	q.count++
	q.bytes += int64(len(data))
	return nil
}

// fileName orders events FIFO by collection time, with the UUID as a tiebreaker
// so two events in the same nanosecond never collide.
func (q *Queue) fileName(ev collectors.Event) string {
	return fmt.Sprintf("%020d-%s%s", ev.CollectedAt.UnixNano(), ev.ID, fileSuffix)
}

// ReadBatch returns up to maxN of the oldest events (and at most maxBytes of
// payload, always returning at least one event if any are present). The events
// stay on disk until Ack or DeadLetter. A queued file that fails to parse is
// dead-lettered so it cannot wedge the queue.
func (q *Queue) ReadBatch(maxN int, maxBytes int64) (Batch, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	names, err := q.sortedPendingLocked()
	if err != nil {
		return Batch{}, err
	}

	var b Batch
	var total int64
	for _, name := range names {
		if maxN > 0 && len(b.Events) >= maxN {
			break
		}
		path := filepath.Join(q.dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // raced with a drop/ack
			}
			return Batch{}, fmt.Errorf("buffer: read %s: %w", name, err)
		}
		if maxBytes > 0 && len(b.Events) > 0 && total+int64(len(data)) > maxBytes {
			break
		}
		var ev collectors.Event
		if err := json.Unmarshal(data, &ev); err != nil {
			log.Printf("buffer: corrupt queued event %s, dead-lettering: %v", name, err)
			_ = q.deadLetterFileLocked(path, int64(len(data)))
			continue
		}
		b.Events = append(b.Events, ev)
		b.files = append(b.files, path)
		total += int64(len(data))
	}
	return b, nil
}

// Ack removes a successfully delivered batch from the queue.
func (q *Queue) Ack(b Batch) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	var firstErr error
	for _, path := range b.files {
		size := fileSize(path)
		if err := os.Remove(path); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("buffer: ack %s: %w", filepath.Base(path), err)
			}
			continue
		}
		q.count--
		q.bytes -= size
	}
	return firstErr
}

// DeadLetter moves a permanently rejected ("poison") batch out of the main
// queue into the dead-letter directory for inspection, so it is neither
// delivered nor retried forever.
func (q *Queue) DeadLetter(b Batch) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	var firstErr error
	for _, path := range b.files {
		if err := q.deadLetterFileLocked(path, fileSize(path)); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (q *Queue) deadLetterFileLocked(path string, size int64) error {
	dest := filepath.Join(q.deadDir, filepath.Base(path))
	if err := os.Rename(path, dest); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("buffer: dead-letter %s: %w", filepath.Base(path), err)
	}
	q.count--
	q.bytes -= size
	return nil
}

// Len reports the number of pending events.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.count
}

// Dropped reports how many events have been discarded because the queue was
// full since it was opened.
func (q *Queue) Dropped() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.dropped
}

func (q *Queue) enforceLimitsLocked(incoming int64) error {
	for q.overLimitLocked(incoming) {
		dropped, err := q.dropOldestLocked()
		if err != nil {
			return err
		}
		if !dropped {
			break // nothing left to drop (e.g. a single event over MaxBytes)
		}
	}
	return nil
}

func (q *Queue) overLimitLocked(incoming int64) bool {
	if q.limits.MaxEvents > 0 && q.count+1 > q.limits.MaxEvents {
		return true
	}
	if q.limits.MaxBytes > 0 && q.bytes+incoming > q.limits.MaxBytes {
		return true
	}
	return false
}

func (q *Queue) dropOldestLocked() (bool, error) {
	names, err := q.sortedPendingLocked()
	if err != nil {
		return false, err
	}
	if len(names) == 0 {
		return false, nil
	}
	path := filepath.Join(q.dir, names[0])
	size := fileSize(path)
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("buffer: drop oldest: %w", err)
	}
	q.count--
	q.bytes -= size
	q.dropped++
	log.Printf("buffer: queue full, dropped oldest event %s (%d dropped total)",
		names[0], q.dropped)
	return true, nil
}

func (q *Queue) sortedPendingLocked() ([]string, error) {
	entries, err := os.ReadDir(q.dir)
	if err != nil {
		return nil, fmt.Errorf("buffer: list queue: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), fileSuffix) {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
