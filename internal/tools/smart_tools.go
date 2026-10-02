package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"freshdesk-mcp-connector/internal/brief"
	"freshdesk-mcp-connector/internal/freshdesk"
	"freshdesk-mcp-connector/internal/mcp"
	"freshdesk-mcp-connector/internal/shield"
	"freshdesk-mcp-connector/internal/similar"
)

const (
	conversationPageSize = 100
	briefMaxPages        = 3 // up to 300 messages; beyond that the brief says the thread is incomplete
	similarPageSize      = 100
	similarMinScore      = 0.1
)

// ---- freshdesk_ticket_brief ------------------------------------------------

func (t *toolset) ticketBrief() mcp.Tool {
	return mcp.Tool{
		Name: "freshdesk_ticket_brief",
		Description: "A short, rule-based summary of one ticket: who is waiting and for how long, the customer's last message, " +
			"whether it is past its due time, warning signs, and a suggested next step. Computed locally from the ticket and its replies; no AI service is used. " +
			"The suggestion is a hint for a person or agent to review, not an action taken." + untrustedNote,
		InputSchema: object(props{"ticket_id": ticketID}, "ticket_id"),
		Handler:     t.handleBrief,
	}
}

func (t *toolset) handleBrief(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		TicketID int64 `json:"ticket_id"`
	}
	if err := decode(raw, &a); err != nil {
		return "", err
	}
	tk, err := t.API.GetTicket(ctx, a.TicketID, false)
	if err != nil {
		return "", toolError(err)
	}
	convs, complete, err := t.allConversations(ctx, a.TicketID)
	if err != nil {
		return "", toolError(err)
	}
	// Mask before summarising so the brief can only repeat masked text.
	t.prepare(tk)
	for i := range convs {
		t.prepareConversation(&convs[i])
	}
	notes := tk.SecurityNotes
	for _, c := range convs {
		notes = shield.Merge(notes, c.SecurityNotes)
	}
	bf := brief.Build(*tk, convs, complete, t.Now())
	bf.Subject = shield.Fence(bf.Subject)
	bf.LastCustomerMessage = shield.Fence(bf.LastCustomerMessage)
	out := struct {
		Brief         brief.Brief    `json:"brief"`
		SecurityNotes []string       `json:"security_notes,omitempty"`
		RateLimit     *rateLimitInfo `json:"rate_limit,omitempty"`
	}{bf, notes, t.rateLimit()}
	b, _ := marshal(out)
	return string(b), nil
}

// allConversations reads pages until the thread ends or the page budget runs out.
func (t *toolset) allConversations(ctx context.Context, id int64) (convs []freshdesk.Conversation, complete bool, err error) {
	for page := 1; page <= briefMaxPages; page++ {
		batch, err := t.API.ListConversations(ctx, id, page, conversationPageSize)
		if err != nil {
			return nil, false, err
		}
		convs = append(convs, batch...)
		if len(batch) < conversationPageSize {
			return convs, true, nil
		}
	}
	return convs, false, nil
}

// ---- freshdesk_find_similar_tickets ----------------------------------------

func (t *toolset) findSimilar() mcp.Tool {
	return mcp.Tool{
		Name: "freshdesk_find_similar_tickets",
		Description: "Find tickets that look related to a given ticket by comparing their words (TF-IDF), and say which shared words caused each match. " +
			"It compares against the most recently updated tickets Freshdesk returns (by default only the last 30 days; pass updated_since to look further back), " +
			"so it is a quick lead, not a complete duplicate check. Computed locally; no AI service is used." + untrustedNote,
		InputSchema: object(props{
			"ticket_id":       ticketID,
			"limit":           integer("How many matches to return.", 1, 10, 5),
			"candidate_pages": integer("How many pages of 100 recent tickets to compare against.", 1, 3, 2),
			"updated_since":   text("Compare against tickets updated after this date, as 2026-01-31."),
		}, "ticket_id"),
		Handler: t.handleSimilar,
	}
}

type similarMatch struct {
	TicketID    int64    `json:"ticket_id"`
	Subject     string   `json:"subject"`
	Status      string   `json:"status"`
	UpdatedAt   string   `json:"updated_at,omitempty"`
	Score       float64  `json:"score"`
	SharedTerms []string `json:"shared_terms"`
	// SecurityNotes is set when that ticket's text looked like an attack.
	SecurityNotes []string `json:"security_notes,omitempty"`
}

func (t *toolset) handleSimilar(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		TicketID       int64  `json:"ticket_id"`
		Limit          int    `json:"limit"`
		CandidatePages int    `json:"candidate_pages"`
		UpdatedSince   string `json:"updated_since"`
	}
	if err := decode(raw, &a); err != nil {
		return "", err
	}
	if a.Limit == 0 {
		a.Limit = 5
	}
	if a.CandidatePages == 0 {
		a.CandidatePages = 2
	}
	if a.Limit < 1 || a.Limit > 10 || a.CandidatePages < 1 || a.CandidatePages > 3 {
		return "", &mcp.ToolError{Kind: "invalid_input", Message: "limit must be 1 to 10 and candidate_pages 1 to 3."}
	}
	opts := freshdesk.ListOptions{PerPage: similarPageSize, Include: []string{"description"}, OrderBy: "updated_at", OrderType: "desc"}
	if a.UpdatedSince != "" {
		when, err := parseWhen(a.UpdatedSince)
		if err != nil {
			return "", err
		}
		opts.UpdatedSince = when
	}

	target, err := t.API.GetTicket(ctx, a.TicketID, false)
	if err != nil {
		return "", toolError(err)
	}
	candidates, err := t.recentTickets(ctx, opts, a.CandidatePages)
	if err != nil {
		return "", toolError(err)
	}

	// Mask first: the words we compare, and the shared words we report, must
	// come from masked text so no part of an email or phone number can leak.
	t.prepare(target)
	byID := map[int64]*freshdesk.Ticket{}
	docs := make([]similar.Doc, 0, len(candidates))
	for i := range candidates {
		t.prepare(&candidates[i])
		byID[candidates[i].ID] = &candidates[i]
		docs = append(docs, similar.Doc{ID: candidates[i].ID, Subject: candidates[i].Subject, Body: candidates[i].DescriptionText})
	}
	ranked := similar.Rank(similar.Doc{ID: target.ID, Subject: target.Subject, Body: target.DescriptionText}, docs, a.Limit, similarMinScore)

	matches := make([]similarMatch, 0, len(ranked))
	for _, m := range ranked {
		c := byID[m.ID]
		matches = append(matches, similarMatch{m.ID, shield.Fence(clip(c.Subject, 150)), c.StatusLabel, c.UpdatedAt, m.Score, m.SharedTerms, c.SecurityNotes})
	}
	return fit(matches, t.MaxResultBytes, func(shown []similarMatch, cut bool) any {
		return struct {
			TicketID     int64          `json:"ticket_id"`
			ComparedWith int            `json:"compared_with"`
			Note         string         `json:"note"`
			RateLimit    *rateLimitInfo `json:"rate_limit,omitempty"`
			Matches      []similarMatch `json:"matches"`
		}{target.ID, len(candidates), fmt.Sprintf("Compared with %d recent tickets. Matches are leads to check by hand. %s", len(candidates), noteIf(cut)), t.rateLimit(), shown}
	}), nil
}

func (t *toolset) recentTickets(ctx context.Context, opts freshdesk.ListOptions, pages int) ([]freshdesk.Ticket, error) {
	var all []freshdesk.Ticket
	for p := 1; p <= pages; p++ {
		opts.Page = p
		page, err := t.API.ListTickets(ctx, opts)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Tickets...)
		if !page.HasMore {
			break
		}
	}
	return all, nil
}
