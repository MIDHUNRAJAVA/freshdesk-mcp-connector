package audit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRecordWritesOneJSONLine(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf)
	l.Record(Entry{Tool: "freshdesk_get_ticket", ArgNames: []string{"ticket_id"}, DurationMS: 12, ResultBytes: 340}, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	l.Record(Entry{Tool: "freshdesk_search_tickets", IsError: true, ErrorKind: "rate_limited"}, time.Now())

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d", len(lines))
	}
	var e Entry
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatal(err)
	}
	if e.Time != "2026-01-02T03:04:05Z" || e.Tool != "freshdesk_get_ticket" || e.ResultBytes != 340 || e.ArgNames[0] != "ticket_id" {
		t.Fatalf("entry = %+v", e)
	}
}

func TestNilLoggerIsSafe(t *testing.T) {
	var l *Logger
	l.Record(Entry{Tool: "x"}, time.Now())
	if New(nil) != nil {
		t.Fatal("nil writer should give a nil logger")
	}
}
