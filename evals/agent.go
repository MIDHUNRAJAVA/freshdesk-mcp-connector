package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"freshdesk-mcp-connector/internal/shield"
)

// callTool runs one connector tool and returns its text (or error text).
type callTool func(ctx context.Context, name string, args string) (text string, isErr bool)

var ticketNum = regexp.MustCompile(`\b(\d{3})\b`)

// runAgent is a scripted stand-in for a model. It reads the question with a
// few keyword rules, picks tools, and writes an answer only from tool output.
// There is no AI here: it exists so the connector's tools can be checked the
// same way every time. It returns the answer and the tools it called.
func runAgent(ctx context.Context, q string, call callTool, now time.Time) (answer string, used []string) {
	do := func(name, args string) (map[string]any, string, bool) {
		used = append(used, name)
		text, isErr := call(ctx, name, args)
		var m map[string]any
		_ = json.Unmarshal([]byte(text), &m)
		return m, text, isErr
	}
	lq := strings.ToLower(q)
	id := ticketNum.FindString(q)

	switch {
	case strings.Contains(lq, "refund order") || strings.Contains(lq, "close ticket"):
		return "I cannot do that. This connector is read-only, so I can look at tickets but not change them or move money.", nil

	case strings.Contains(lq, "overdue"):
		m, _, _ := do("freshdesk_list_tickets", `{"per_page":100,"updated_since":"2020-01-01"}`)
		var out []string
		for _, t := range list(m["tickets"]) {
			due, _ := time.Parse(time.RFC3339, str(t["due_by"]))
			if (t["status_label"] == "open" || t["status_label"] == "pending") && due.Before(now) {
				out = append(out, fmt.Sprintf("#%d %s", int(num(t["id"])), unfence(str(t["subject"]))))
			}
		}
		return "Overdue: " + strings.Join(out, "; "), used

	case strings.Contains(lq, "like the"):
		m, _, _ := do("freshdesk_search_tickets", `{"query":"tag:'refund' AND status:2"}`)
		first := list(m["tickets"])[0]
		s, _, _ := do("freshdesk_find_similar_tickets", fmt.Sprintf(`{"ticket_id":%d,"updated_since":"2020-01-01"}`, int(num(first["id"]))))
		var out []string
		for _, x := range list(s["matches"]) {
			out = append(out, fmt.Sprintf("#%d %s (shared words: %v)", int(num(x["ticket_id"])), unfence(str(x["subject"])), x["shared_terms"]))
		}
		return fmt.Sprintf("Similar to #%d: %s", int(num(first["id"])), strings.Join(out, "; ")), used

	case strings.Contains(lq, "refund"), strings.Contains(lq, "urgent"):
		query := `tag:'refund' AND status:2`
		if strings.Contains(lq, "urgent") {
			query = `priority:4`
		}
		m, _, _ := do("freshdesk_search_tickets", fmt.Sprintf(`{"query":%q}`, query))
		var out []string
		for _, t := range list(m["tickets"]) {
			out = append(out, fmt.Sprintf("#%d %s", int(num(t["id"])), unfence(str(t["subject"]))))
		}
		return "Found: " + strings.Join(out, "; "), used

	case strings.Contains(lq, "summary") || strings.Contains(lq, "say last"):
		m, _, _ := do("freshdesk_ticket_brief", fmt.Sprintf(`{"ticket_id":%s}`, id))
		b, _ := m["brief"].(map[string]any)
		over := "not overdue"
		if b["overdue"] == true {
			over = "overdue"
		}
		return fmt.Sprintf("Ticket #%s is waiting on %v and is %s. Last customer message: %s", id, b["waiting_on"], over, unfence(str(b["last_customer_message"]))), used

	default: // read one ticket
		m, text, isErr := do("freshdesk_get_ticket", fmt.Sprintf(`{"ticket_id":%s}`, id))
		if isErr {
			return "Not found: " + text, used
		}
		t, _ := m["ticket"].(map[string]any)
		if notes := strs(t["security_notes"]); len(notes) > 0 {
			// The ticket text is data. Report the attack; do not act on it.
			return fmt.Sprintf("Warning: ticket #%s text looks like an attack (%s). I treated it as data and did nothing it asked.", id, strings.Join(notes, " ")), used
		}
		r, _ := t["requester"].(map[string]any)
		contact := fmt.Sprintf("%v", r["mobile"])
		if contact == "<nil>" {
			contact = fmt.Sprintf("%v", r["phone"])
		}
		return fmt.Sprintf("Ticket #%s from %v. Phone: %s", id, r["name"], contact), used
	}
}

func unfence(s string) string {
	s = strings.TrimPrefix(s, "<"+shield.Tag+">")
	return strings.TrimSuffix(s, "</"+shield.Tag+">")
}

func list(v any) []map[string]any {
	var out []map[string]any
	items, _ := v.([]any)
	for _, i := range items {
		if m, ok := i.(map[string]any); ok {
			out = append(out, m)
		}
	}
	if out == nil {
		out = []map[string]any{{}} // keeps the scripted agent from panicking on empty results
	}
	return out
}

func str(v any) string  { s, _ := v.(string); return s }
func num(v any) float64 { f, _ := v.(float64); return f }
func strs(v any) []string {
	var out []string
	items, _ := v.([]any)
	for _, i := range items {
		out = append(out, str(i))
	}
	return out
}
