// Package audit writes one JSON line per tool call, so a human can see what
// the agent did. It records the tool name, timing, size and outcome. It
// never records arguments or results, because those can hold customer data.
package audit

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// Entry is one audit line. Every field is safe to store.
type Entry struct {
	Time        string   `json:"time"`
	Tool        string   `json:"tool"`
	ArgNames    []string `json:"arg_names,omitempty"` // names only, never values
	DurationMS  int64    `json:"duration_ms"`
	ResultBytes int      `json:"result_bytes"`
	IsError     bool     `json:"is_error"`
	ErrorKind   string   `json:"error_kind,omitempty"`
}

// Logger is safe for concurrent use. A nil *Logger discards everything.
type Logger struct {
	mu sync.Mutex
	w  io.Writer
}

func New(w io.Writer) *Logger {
	if w == nil {
		return nil
	}
	return &Logger{w: w}
}

func (l *Logger) Record(e Entry, at time.Time) {
	if l == nil {
		return
	}
	e.Time = at.UTC().Format(time.RFC3339)
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.w.Write(append(line, '\n'))
}
