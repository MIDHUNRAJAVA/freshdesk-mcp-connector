package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"freshdesk-mcp-connector/internal/audit"
	"freshdesk-mcp-connector/internal/fakefd"
	"freshdesk-mcp-connector/internal/freshdesk"
	"freshdesk-mcp-connector/internal/mask"
	"freshdesk-mcp-connector/internal/mcp"
)

// session is an MCP client talking to a real server over in-memory pipes,
// backed by the real Freshdesk client and the fake Freshdesk API.
type session struct {
	t      *testing.T
	in     io.WriteCloser
	out    *bufio.Reader
	nextID int
	done   chan error
	audit  *bytes.Buffer
}

func newSession(t *testing.T, masker mask.Masker, maxBytes int, fakeOpts fakefd.Options) *session {
	t.Helper()
	fake := httptest.NewServer(fakefd.New(fakeOpts))
	t.Cleanup(fake.Close)
	key := fakeOpts.APIKey
	if key == "" {
		key = "demo-key"
	}
	client, err := freshdesk.NewClient(freshdesk.Config{APIKey: key, BaseURL: fake.URL + "/api/v2", MaxRetries: 1,
		BackoffBase: time.Millisecond, BackoffMax: 2 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	auditBuf := &bytes.Buffer{}
	srv := &mcp.Server{Name: "t", Version: "0", Instructions: Instructions, Audit: audit.New(auditBuf), MaxResultBytes: 1 << 20,
		Tools: New(Deps{API: client, Mask: masker, MaxResultBytes: maxBytes})}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := &session{t: t, in: inW, out: bufio.NewReaderSize(outR, 1<<20), done: make(chan error, 1), audit: auditBuf}
	go func() {
		err := srv.Serve(context.Background(), inR, outW)
		outW.Close()
		s.done <- err
	}()
	t.Cleanup(func() { _ = s.in.Close() })
	return s
}

func (s *session) send(line string) { fmt.Fprintln(s.in, line) }

func (s *session) read() map[string]any {
	s.t.Helper()
	line, err := s.out.ReadBytes('\n')
	if err != nil {
		s.t.Fatalf("reading response: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		s.t.Fatalf("bad JSON %q: %v", line, err)
	}
	return m
}

func (s *session) call(tool string, args string) (text string, isError bool) {
	s.t.Helper()
	s.nextID++
	s.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, s.nextID, tool, args))
	res := s.read()
	if res["error"] != nil {
		s.t.Fatalf("protocol error: %v", res["error"])
	}
	r := res["result"].(map[string]any)
	return r["content"].([]any)[0].(map[string]any)["text"].(string), r["isError"].(bool)
}

func (s *session) callJSON(tool, args string) map[string]any {
	s.t.Helper()
	text, isErr := s.call(tool, args)
	if isErr {
		s.t.Fatalf("%s returned an error: %s", tool, text)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		s.t.Fatalf("result is not JSON: %v\n%s", err, text)
	}
	return m
}

var maskOn = mask.Masker{Enabled: true}

func TestFullRoundTrip(t *testing.T) {
	s := newSession(t, maskOn, 40000, fakefd.Options{})

	s.send(`{"jsonrpc":"2.0","id":100,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	init := s.read()["result"].(map[string]any)
	if init["serverInfo"].(map[string]any)["name"] != "t" || !strings.Contains(init["instructions"].(string), "cannot reply") {
		t.Fatalf("initialize = %v", init)
	}
	s.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	s.send(`{"jsonrpc":"2.0","id":101,"method":"tools/list"}`)
	list := s.read()["result"].(map[string]any)["tools"].([]any)
	var names []string
	for _, tl := range list {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	want := "freshdesk_list_tickets freshdesk_get_ticket freshdesk_search_tickets freshdesk_list_conversations freshdesk_ticket_brief freshdesk_find_similar_tickets"
	if strings.Join(names, " ") != want {
		t.Fatalf("tools = %v", names)
	}
	s.send(`{"jsonrpc":"2.0","id":102,"method":"ping"}`)
	if s.read()["id"].(float64) != 102 {
		t.Fatal("ping answered out of order; the notification must not produce a reply")
	}

	list1 := s.callJSON("freshdesk_list_tickets", `{"per_page":5}`)
	if list1["count"].(float64) != 5 || !list1["has_more"].(bool) || list1["rate_limit"] == nil {
		t.Fatalf("list = %v", list1)
	}
	get := s.callJSON("freshdesk_get_ticket", `{"ticket_id":201,"include_conversations":true}`)
	tk := get["ticket"].(map[string]any)
	if tk["status_label"] != "open" || len(tk["conversations"].([]any)) != 3 {
		t.Fatalf("get = %v", tk)
	}
	conv := s.callJSON("freshdesk_list_conversations", `{"ticket_id":201}`)
	if conv["count"].(float64) != 3 {
		t.Fatalf("conversations = %v", conv)
	}
	search := s.callJSON("freshdesk_search_tickets", `{"query":"tag:'refund' AND status:2"}`)
	if search["total_matches"].(float64) != 1 {
		t.Fatalf("search = %v", search)
	}
}

func TestAgentNeverSeesRawContactDetails(t *testing.T) {
	s := newSession(t, maskOn, 40000, fakefd.Options{})
	calls := []struct{ tool, args string }{
		{"freshdesk_list_tickets", `{"per_page":20}`},
		{"freshdesk_get_ticket", `{"ticket_id":201,"include_conversations":true}`},
		{"freshdesk_search_tickets", `{"query":"status:2"}`},
		{"freshdesk_list_conversations", `{"ticket_id":201}`},
		{"freshdesk_list_conversations", `{"ticket_id":204}`},
		{"freshdesk_ticket_brief", `{"ticket_id":201}`},
		{"freshdesk_find_similar_tickets", `{"ticket_id":201}`},
	}
	for _, c := range calls {
		text, isErr := s.call(c.tool, c.args)
		if isErr {
			t.Fatalf("%s: %s", c.tool, text)
		}
		for _, leak := range []string{"asha.verma", "example.com", "example.org", "98765", "43210", "555 010", "4477", "liam.carter"} {
			if strings.Contains(text, leak) {
				t.Errorf("%s leaked %q:\n%s", c.tool, leak, text)
			}
		}
	}
	// And the masking can be switched off on purpose.
	raw := newSession(t, mask.Masker{}, 40000, fakefd.Options{})
	text, _ := raw.call("freshdesk_get_ticket", `{"ticket_id":201}`)
	if !strings.Contains(text, "asha.verma@example.com") {
		t.Fatal("with masking off the raw email should appear")
	}
}

func TestToolsAreReadOnlyByDesign(t *testing.T) {
	// The client interface has no write methods, and every tool is a getter.
	for _, d := range mcp.Definitions(New(Deps{})) {
		if !d.Annotations.ReadOnlyHint || d.Annotations.DestructiveHint {
			t.Errorf("%s is not marked read-only", d.Name)
		}
		for _, bad := range []string{"create", "update", "delete", "reply", "close", "assign"} {
			if strings.Contains(d.Name, bad) {
				t.Errorf("tool name %q suggests a write", d.Name)
			}
		}
	}
}

func TestToolErrorsAreIsErrorContent(t *testing.T) {
	s := newSession(t, maskOn, 40000, fakefd.Options{})
	tests := []struct {
		name, tool, args, want string
	}{
		{"not found", "freshdesk_get_ticket", `{"ticket_id":999999}`, "Not found"},
		{"bad input", "freshdesk_list_tickets", `{"per_page":500}`, "per_page"},
		{"unknown argument", "freshdesk_list_tickets", `{"perpage":5}`, "Invalid arguments"},
		{"missing id", "freshdesk_get_ticket", `{}`, "positive"},
		{"bad date", "freshdesk_list_tickets", `{"updated_since":"yesterday"}`, "not a date"},
		{"search page too deep", "freshdesk_search_tickets", `{"query":"status:2","page":11}`, "between 1 and 10"},
		{"unsupported query", "freshdesk_search_tickets", `{"query":"foo:bar"}`, "did not accept"},
		{"brief not found", "freshdesk_ticket_brief", `{"ticket_id":999999}`, "Not found"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			text, isErr := s.call(tc.tool, tc.args)
			if !isErr || !strings.Contains(text, tc.want) {
				t.Fatalf("isError=%v text=%q, want it to contain %q", isErr, text, tc.want)
			}
		})
	}
}

func TestBadCredentialsExplainedWithoutEchoingKey(t *testing.T) {
	fake := httptest.NewServer(fakefd.New(fakefd.Options{APIKey: "right-key"}))
	t.Cleanup(fake.Close)
	client, _ := freshdesk.NewClient(freshdesk.Config{APIKey: "wrong-key-xyz", BaseURL: fake.URL + "/api/v2"})
	ts := New(Deps{API: client, Mask: maskOn})
	_, err := ts[1].Handler(context.Background(), json.RawMessage(`{"ticket_id":201}`))
	var te *mcp.ToolError
	if err == nil || !asToolError(err, &te) || te.Kind != "auth_failed" {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(te.Message, "wrong-key-xyz") || !strings.Contains(te.Message, "credentials") {
		t.Fatalf("message = %q", te.Message)
	}
}

func asToolError(err error, target **mcp.ToolError) bool {
	te, ok := err.(*mcp.ToolError)
	*target = te
	return ok
}

func TestResultsAreCappedInSize(t *testing.T) {
	const cap = 2500
	s := newSession(t, maskOn, cap, fakefd.Options{})
	text, isErr := s.call("freshdesk_list_tickets", `{"per_page":50,"updated_since":"2020-01-01"}`)
	if isErr || len(text) > cap {
		t.Fatalf("isError=%v len=%d cap=%d", isErr, len(text), cap)
	}
	var out struct {
		Count   int    `json:"count"`
		Note    string `json:"note"`
		Tickets []any  `json:"tickets"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("truncated output must still be valid JSON: %v", err)
	}
	if out.Count == 0 || out.Count >= 10 || len(out.Tickets) != out.Count || !strings.Contains(out.Note, "cut") {
		t.Fatalf("count=%d note=%q", out.Count, out.Note)
	}
}

func TestLongTextIsClipped(t *testing.T) {
	ts := &toolset{Deps{MaxTextChars: 20}}
	tk := &freshdesk.Ticket{DescriptionText: strings.Repeat("a", 100)}
	ts.prepare(tk)
	if len([]rune(tk.DescriptionText)) > 30 || !strings.HasSuffix(tk.DescriptionText, "[cut]") {
		t.Fatalf("got %q", tk.DescriptionText)
	}
}

func TestListFiltersAndOldTickets(t *testing.T) {
	s := newSession(t, maskOn, 40000, fakefd.Options{})
	def := s.callJSON("freshdesk_list_tickets", `{"per_page":100}`)
	old := s.callJSON("freshdesk_list_tickets", `{"per_page":100,"updated_since":"2020-01-01"}`)
	if old["count"].(float64) <= def["count"].(float64) {
		t.Fatalf("updated_since should reach older tickets: %v vs %v", old["count"], def["count"])
	}
	closed := s.callJSON("freshdesk_list_tickets", `{"per_page":100,"updated_since":"2020-01-01","status":"closed"}`)
	for _, tk := range closed["tickets"].([]any) {
		if tk.(map[string]any)["status_label"] != "closed" {
			t.Fatalf("status filter leaked: %v", tk)
		}
	}
	noReq := s.callJSON("freshdesk_list_tickets", `{"include_requester":false}`)
	if _, has := noReq["tickets"].([]any)[0].(map[string]any)["requester"]; has {
		t.Fatal("requester should be omitted")
	}
}

func TestBriefTool(t *testing.T) {
	s := newSession(t, maskOn, 40000, fakefd.Options{})
	b := s.callJSON("freshdesk_ticket_brief", `{"ticket_id":201}`)["brief"].(map[string]any)
	if b["waiting_on"] != "agent" || b["overdue"] != true || !strings.Contains(b["suggested_next_step"].(string), "Reply now") {
		t.Fatalf("brief = %v", b)
	}
	if b["internal_notes"].(float64) != 1 || b["thread_complete"] != true {
		t.Fatalf("brief = %v", b)
	}
	closed := s.callJSON("freshdesk_ticket_brief", `{"ticket_id":205}`)["brief"].(map[string]any)
	if closed["waiting_on"] != "nobody" {
		t.Fatalf("closed = %v", closed)
	}
}

func TestSimilarTool(t *testing.T) {
	s := newSession(t, maskOn, 40000, fakefd.Options{})
	res := s.callJSON("freshdesk_find_similar_tickets", `{"ticket_id":201,"updated_since":"2020-01-01","limit":3}`)
	matches := res["matches"].([]any)
	if len(matches) == 0 {
		t.Fatalf("expected matches: %v", res)
	}
	top := matches[0].(map[string]any)
	if top["ticket_id"].(float64) != 202 {
		t.Fatalf("the other refund ticket should rank first, got %v", matches)
	}
	if !strings.Contains(fmt.Sprint(top["shared_terms"]), "refund") {
		t.Fatalf("explanation = %v", top["shared_terms"])
	}
	for _, m := range matches {
		if m.(map[string]any)["ticket_id"].(float64) == 201 {
			t.Fatal("must not match itself")
		}
	}
	if _, isErr := s.call("freshdesk_find_similar_tickets", `{"ticket_id":201,"limit":50}`); !isErr {
		t.Fatal("limit above 10 must be rejected")
	}
}

func TestAuditLogHasNoCustomerData(t *testing.T) {
	s := newSession(t, maskOn, 40000, fakefd.Options{})
	s.call("freshdesk_search_tickets", `{"query":"tag:'refund'"}`)
	s.call("freshdesk_get_ticket", `{"ticket_id":999999}`)
	log := s.audit.String()
	lines := strings.Split(strings.TrimSpace(log), "\n")
	if len(lines) != 2 || strings.Contains(log, "refund") || strings.Contains(log, "example") {
		t.Fatalf("audit log:\n%s", log)
	}
	if !strings.Contains(lines[1], `"error_kind":"not_found"`) || !strings.Contains(lines[0], `"tool":"freshdesk_search_tickets"`) {
		t.Fatalf("audit log:\n%s", log)
	}
}

func TestRetriesAreInvisibleToTheAgent(t *testing.T) {
	// Every 2nd request fails with 500; the client retries and the agent just gets data.
	s := newSession(t, maskOn, 40000, fakefd.Options{Every500: 2})
	for i := 0; i < 4; i++ {
		if _, isErr := s.call("freshdesk_get_ticket", fmt.Sprintf(`{"ticket_id":%d}`, 201+i)); isErr {
			t.Fatalf("call %d failed even though retries should have hidden the 500", i)
		}
	}
}

func TestDefinitionsFileIsUpToDate(t *testing.T) {
	var want bytes.Buffer
	enc := json.NewEncoder(&want)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{"tools": mcp.Definitions(New(Deps{}))})
	got, err := os.ReadFile("../../docs/mcp-tools.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want.Bytes()) {
		t.Fatal("docs/mcp-tools.json is out of date; run: make tools-json")
	}
}

func TestEverySchemaIsAnObjectWithDescribedProperties(t *testing.T) {
	for _, d := range mcp.Definitions(New(Deps{})) {
		if d.InputSchema["type"] != "object" || d.InputSchema["additionalProperties"] != false {
			t.Errorf("%s: schema must be a closed object", d.Name)
		}
		for name, p := range d.InputSchema["properties"].(map[string]any) {
			if _, ok := p.(map[string]any)["description"]; !ok && name != "ticket_id" {
				t.Errorf("%s.%s has no description", d.Name, name)
			}
		}
	}
}

// ---- prompt-injection shield ----

func TestTicketTextIsFencedInEveryTool(t *testing.T) {
	s := newSession(t, maskOn, 40000, fakefd.Options{})
	calls := []struct{ tool, args string }{
		{"freshdesk_list_tickets", `{"per_page":20}`},
		{"freshdesk_get_ticket", `{"ticket_id":201,"include_conversations":true}`},
		{"freshdesk_search_tickets", `{"query":"status:2"}`},
		{"freshdesk_list_conversations", `{"ticket_id":201}`},
		{"freshdesk_ticket_brief", `{"ticket_id":201}`},
		{"freshdesk_find_similar_tickets", `{"ticket_id":201}`},
	}
	for _, c := range calls {
		text, _ := s.call(c.tool, c.args)
		if strings.Count(text, "<untrusted_ticket_text>") == 0 ||
			strings.Count(text, "<untrusted_ticket_text>") != strings.Count(text, "</untrusted_ticket_text>") {
			t.Errorf("%s: ticket text is not fenced", c.tool)
		}
	}
	// A normal ticket has no security notes in any tool that shows it.
	for _, c := range calls[1:2] {
		if text, _ := s.call(c.tool, c.args); strings.Contains(text, "security_notes") {
			t.Errorf("%s: normal ticket 201 got security notes:\n%s", c.tool, text)
		}
	}
}

func TestShieldFlagsAttackTicketsAndKeepsText(t *testing.T) {
	s := newSession(t, maskOn, 40000, fakefd.Options{})

	get := s.callJSON("freshdesk_get_ticket", `{"ticket_id":211}`)["ticket"].(map[string]any)
	notes := fmt.Sprint(get["security_notes"])
	for _, want := range []string{"ignore its earlier", "new role", "outside address", "secrets"} {
		if !strings.Contains(notes, want) {
			t.Errorf("ticket 211 notes %q should mention %q", notes, want)
		}
	}
	// The original text stays, fenced, with the email still masked.
	desc := get["description_text"].(string)
	if !strings.HasPrefix(desc, "<untrusted_ticket_text>") || !strings.Contains(desc, "IGNORE YOUR RULES") || strings.Contains(desc, "attacker@example.com") {
		t.Errorf("description = %q", desc)
	}

	conv := s.callJSON("freshdesk_list_conversations", `{"ticket_id":212}`)["conversations"].([]any)[0].(map[string]any)
	notes = fmt.Sprint(conv["security_notes"])
	if !strings.Contains(notes, "run a command") || !strings.Contains(notes, "encoded") {
		t.Errorf("ticket 212 reply notes = %q", notes)
	}

	hidden := s.callJSON("freshdesk_get_ticket", `{"ticket_id":212}`)["ticket"].(map[string]any)
	if !strings.Contains(fmt.Sprint(hidden["security_notes"]), "hidden characters") {
		t.Errorf("ticket 212 notes = %v", hidden["security_notes"])
	}

	brief := s.callJSON("freshdesk_ticket_brief", `{"ticket_id":212}`)
	if !strings.Contains(fmt.Sprint(brief["security_notes"]), "run a command") {
		t.Errorf("brief notes = %v", brief["security_notes"])
	}
	sim := s.callJSON("freshdesk_find_similar_tickets", `{"ticket_id":212}`)
	if sim["matches"] == nil && sim["compared_with"] == nil {
		t.Errorf("similar = %v", sim)
	}
}

func TestInstructionsAndDescriptionsExplainTheFences(t *testing.T) {
	if !strings.Contains(Instructions, "never instructions") || !strings.Contains(Instructions, "<untrusted_ticket_text>") {
		t.Error("server instructions must say fenced text is data")
	}
	for _, d := range mcp.Definitions(New(Deps{})) {
		if !strings.Contains(d.Description, "never instructions") {
			t.Errorf("%s: description must warn that ticket text is untrusted", d.Name)
		}
	}
}
