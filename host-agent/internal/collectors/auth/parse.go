package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/collectors"
)

// journalFieldMap maps the journald fields we keep to the snake_case keys used in
// Event.Payload. journalctl renders ordinary fields as JSON strings.
var journalFieldMap = map[string]string{
	"MESSAGE":           "message",
	"PRIORITY":          "priority",
	"SYSLOG_FACILITY":   "syslog_facility",
	"SYSLOG_IDENTIFIER": "syslog_identifier",
	"_COMM":             "comm",
	"_EXE":              "exe",
	"_PID":              "pid",
	"_UID":              "uid",
	"_SYSTEMD_UNIT":     "systemd_unit",
	"_HOSTNAME":         "source_hostname",
	"_TRANSPORT":        "transport",
	"_BOOT_ID":          "boot_id",
}

// parseEntry converts one `journalctl -o json` line into an Event plus the
// entry's journald cursor. It returns an error for entries we can't use (not
// JSON, or missing the cursor/timestamp we rely on); callers skip those.
func parseEntry(line []byte) (collectors.Event, string, error) {
	var fields map[string]any
	if err := json.Unmarshal(line, &fields); err != nil {
		return collectors.Event{}, "", fmt.Errorf("invalid json: %w", err)
	}

	cursor := stringField(fields, "__CURSOR")
	if cursor == "" {
		return collectors.Event{}, "", errors.New("missing __CURSOR")
	}

	ts, err := parseRealtime(stringField(fields, "__REALTIME_TIMESTAMP"))
	if err != nil {
		return collectors.Event{}, "", fmt.Errorf("__REALTIME_TIMESTAMP: %w", err)
	}

	return collectors.NewEvent(Name, ts, buildPayload(fields)), cursor, nil
}

// parseRealtime converts journald's __REALTIME_TIMESTAMP (microseconds since the
// Unix epoch, as a string) into a UTC time.
func parseRealtime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, errors.New("empty")
	}
	micros, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.UnixMicro(micros).UTC(), nil
}

func buildPayload(fields map[string]any) map[string]any {
	payload := make(map[string]any, len(journalFieldMap))
	for journalKey, outKey := range journalFieldMap {
		if v := stringField(fields, journalKey); v != "" {
			payload[outKey] = v
		}
	}
	return payload
}

// stringField returns fields[key] if it is a string, else "". journalctl emits
// non-UTF8 blob fields as arrays of byte values; we simply skip those here.
func stringField(fields map[string]any, key string) string {
	if v, ok := fields[key].(string); ok {
		return v
	}
	return ""
}
