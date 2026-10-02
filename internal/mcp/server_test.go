package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"freshdesk-mcp-connector/internal/audit"
)

func testServer(auditBuf io.Writer) *Server {
	return &Server{
		Name: "test", Version: "0.0.1", Instructions: "hi", MaxResultBytes: 100, Audit: audit.New(auditBuf),
		Tools: []Tool{
			{Name: "echo", Description: "echo", InputSchema: map[string]any{"type": "object"},
				Handler: func(ctx context.Context, a json.RawMessage) (string, error) { return "ok", nil }},
			{Name: "typed_fail", InputSchema: map[string]any{"type": "object"},
				Handler: func(ctx context.Context, a json.RawMessage) (string, error) {
					return "", &ToolError{Kind: "not_found", Message: "no such ticket"}
				}},
			{Name: "raw_fail", InputSchema: map[string]any{"type": "object"},
				Handler: func(ctx context.Context, a json.RawMessage) (string, error) {
					return "", errors.New("secret internals")
				}},
			{Name: "big", InputSchema: map[string]any{"type": "object"},
				Handler: func(ctx context.Context, a json.RawMessage) (string, error) {
					return strings.Repeat("x", 500), nil
				}},
		},
	}
}

// run feeds lines to the server and returns each response line.
func run(t *testing.T, s *Server, lines ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	var res []map[string]any
	sc := bufio.NewScanner(&out)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("bad output line %q: %v", sc.Text(), err)
		}
		res = append(res, m)
	}
	return res
}

func TestHandshakeListAndPing(t *testing.T) {
	res := run(t, testServer(nil),
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":"abc","method":"tools/list"}`,
	)
	if len(res) != 3 {
		t.Fatalf("a notification must not get a reply: got %d responses", len(res))
	}
	init := res[0]["result"].(map[string]any)
	if init["protocolVersion"] != "2025-03-26" || init["instructions"] != "hi" {
		t.Fatalf("initialize = %v", init)
	}
	if res[1]["id"].(float64) != 2 || len(res[1]["result"].(map[string]any)) != 0 {
		t.Fatalf("ping = %v", res[1])
	}
	if res[2]["id"] != "abc" {
		t.Fatal("string ids must be echoed back")
	}
	tools := res[2]["result"].(map[string]any)["tools"].([]any)
	first := tools[0].(map[string]any)
	if len(tools) != 4 || first["annotations"].(map[string]any)["readOnlyHint"] != true {
		t.Fatalf("tools = %v", tools)
	}
}

func TestUnknownProtocolVersionFallsBack(t *testing.T) {
	res := run(t, testServer(nil), `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`)
	if got := res[0]["result"].(map[string]any)["protocolVersion"]; got != defaultProtocolVersion {
		t.Fatalf("version = %v", got)
	}
}

func TestToolCallResults(t *testing.T) {
	var auditBuf bytes.Buffer
	res := run(t, testServer(&auditBuf),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"q":"secret customer text"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"typed_fail"}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"raw_fail"}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"big"}}`,
	)
	tests := []struct {
		i       int
		isError bool
		text    string
	}{
		{0, false, "ok"},
		{1, true, "no such ticket"},
		{2, true, "unexpectedly"},
		{3, true, "limit"},
	}
	for _, tc := range tests {
		r := res[tc.i]["result"].(map[string]any)
		text := r["content"].([]any)[0].(map[string]any)["text"].(string)
		if r["isError"] != tc.isError || !strings.Contains(text, tc.text) {
			t.Errorf("call %d: isError=%v text=%q", tc.i, r["isError"], text)
		}
		if strings.Contains(text, "secret internals") {
			t.Errorf("raw error text must not reach the agent: %q", text)
		}
	}
	log := auditBuf.String()
	if strings.Count(log, "\n") != 4 || strings.Contains(log, "secret customer text") {
		t.Fatalf("audit log wrong or leaks arguments:\n%s", log)
	}
	for _, want := range []string{`"arg_names":["q"]`, `"error_kind":"not_found"`, `"error_kind":"internal"`, `"error_kind":"result_too_large"`} {
		if !strings.Contains(log, want) {
			t.Errorf("audit log missing %s:\n%s", want, log)
		}
	}
}

func TestProtocolErrors(t *testing.T) {
	res := run(t, testServer(nil),
		`not json`,
		`{"jsonrpc":"1.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":2,"method":"nope"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"missing"}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{}}`,
		``,
		`{"jsonrpc":"2.0","method":"nope"}`,
	)
	wantCodes := []float64{-32700, -32600, -32601, -32602, -32602}
	if len(res) != len(wantCodes) {
		t.Fatalf("got %d responses, want %d: %v", len(res), len(wantCodes), res)
	}
	for i, code := range wantCodes {
		if got := res[i]["error"].(map[string]any)["code"].(float64); got != code {
			t.Errorf("response %d code = %v, want %v", i, got, code)
		}
	}
	if res[0]["id"] != nil {
		t.Error("parse errors must use a null id")
	}
}

func TestStopsWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := testServer(nil).Serve(ctx, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"), io.Discard)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}
