package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"freshdesk-mcp-connector/internal/freshdesk"
	"freshdesk-mcp-connector/internal/mcp"
)

// parseWhen accepts a full timestamp or just a date.
func parseWhen(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, &mcp.ToolError{Kind: "invalid_input", Message: fmt.Sprintf("updated_since %q is not a date; use 2026-01-31 or 2026-01-31T09:00:00Z", s)}
}

// ---- freshdesk_list_tickets ------------------------------------------------

func (t *toolset) listTickets() mcp.Tool {
	return mcp.Tool{
		Name: "freshdesk_list_tickets",
		Description: "List tickets, newest activity first by default. Returns one page. By default Freshdesk only lists tickets created in the last 30 days; " +
			"pass updated_since to reach older ones. The status filter applies to the returned page only; use freshdesk_search_tickets to filter across all tickets.",
		InputSchema: object(props{
			"page":              integer("Page number, starting at 1.", 1, 1000, 1),
			"per_page":          integer("Tickets per page.", 1, 100, 20),
			"updated_since":     text("Only tickets updated after this time, as 2026-01-31 or 2026-01-31T09:00:00Z."),
			"status":            enum("Keep only tickets with this status (applied to this page).", "open", "pending", "resolved", "closed"),
			"filter":            enum("A Freshdesk preset list.", "new_and_my_open", "watching", "spam", "deleted"),
			"order_by":          enum("Sort field.", "created_at", "due_by", "updated_at", "status"),
			"order_type":        enum("Sort direction.", "asc", "desc"),
			"include_requester": boolean("Include the requester (name, masked email and phone).", true),
		}),
		Handler: t.handleList,
	}
}

func (t *toolset) handleList(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Page             int    `json:"page"`
		PerPage          int    `json:"per_page"`
		UpdatedSince     string `json:"updated_since"`
		Status           string `json:"status"`
		Filter           string `json:"filter"`
		OrderBy          string `json:"order_by"`
		OrderType        string `json:"order_type"`
		IncludeRequester *bool  `json:"include_requester"`
	}
	if err := decode(raw, &a); err != nil {
		return "", err
	}
	opts := freshdesk.ListOptions{Page: a.Page, PerPage: a.PerPage, Filter: a.Filter, OrderBy: a.OrderBy, OrderType: a.OrderType}
	if opts.PerPage == 0 {
		opts.PerPage = 20
	}
	if a.UpdatedSince != "" {
		when, err := parseWhen(a.UpdatedSince)
		if err != nil {
			return "", err
		}
		opts.UpdatedSince = when
	}
	if a.Status != "" {
		opts.Status, _ = freshdesk.StatusCode(a.Status)
	}
	if a.IncludeRequester == nil || *a.IncludeRequester {
		opts.Include = []string{"requester"}
	}

	page, err := t.API.ListTickets(ctx, opts)
	if err != nil {
		return "", toolError(err)
	}
	for i := range page.Tickets {
		t.prepare(&page.Tickets[i])
	}
	return fit(page.Tickets, t.MaxResultBytes, func(shown []freshdesk.Ticket, cut bool) any {
		return struct {
			Page      int                `json:"page"`
			PerPage   int                `json:"per_page"`
			Count     int                `json:"count"`
			HasMore   bool               `json:"has_more"`
			Note      string             `json:"note,omitempty"`
			RateLimit *rateLimitInfo     `json:"rate_limit,omitempty"`
			Tickets   []freshdesk.Ticket `json:"tickets"`
		}{page.Page, page.PerPage, len(shown), page.HasMore, noteIf(cut), t.rateLimit(), shown}
	}), nil
}

// ---- freshdesk_get_ticket --------------------------------------------------

func (t *toolset) getTicket() mcp.Tool {
	return mcp.Tool{
		Name: "freshdesk_get_ticket",
		Description: "Get one ticket by number, with its requester. Optionally include the first batch of replies; " +
			"Freshdesk limits how many it embeds, so use freshdesk_list_conversations for a long thread.",
		InputSchema: object(props{
			"ticket_id":             ticketID,
			"include_conversations": boolean("Also include the first replies and notes.", false),
		}, "ticket_id"),
		Handler: t.handleGet,
	}
}

func (t *toolset) handleGet(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		TicketID             int64 `json:"ticket_id"`
		IncludeConversations bool  `json:"include_conversations"`
	}
	if err := decode(raw, &a); err != nil {
		return "", err
	}
	tk, err := t.API.GetTicket(ctx, a.TicketID, a.IncludeConversations)
	if err != nil {
		return "", toolError(err)
	}
	t.prepare(tk)
	convs := tk.Conversations
	return fit(convs, t.MaxResultBytes, func(shown []freshdesk.Conversation, cut bool) any {
		copyOf := *tk
		copyOf.Conversations = shown
		return struct {
			Note      string            `json:"note,omitempty"`
			RateLimit *rateLimitInfo    `json:"rate_limit,omitempty"`
			Ticket    *freshdesk.Ticket `json:"ticket"`
		}{noteIf(cut), t.rateLimit(), &copyOf}
	}), nil
}

// ---- freshdesk_search_tickets ----------------------------------------------

func (t *toolset) searchTickets() mcp.Tool {
	return mcp.Tool{
		Name: "freshdesk_search_tickets",
		Description: "Search tickets with Freshdesk's query syntax, for example: status:2 AND priority:3, or tag:'refund', or created_at:>'2026-01-01'. " +
			"Use single quotes for text values. Results come 30 per page and Freshdesk allows at most 10 pages, so at most 300 matches are reachable; narrow the query instead of paging deeper. " +
			"Status codes: 2 open, 3 pending, 4 resolved, 5 closed. Priority codes: 1 low, 2 medium, 3 high, 4 urgent.",
		InputSchema: object(props{
			"query": text("Freshdesk search query, without surrounding double quotes. Max 500 characters."),
			"page":  integer("Page number, 1 to 10.", 1, freshdesk.MaxSearchPages, 1),
		}, "query"),
		Handler: t.handleSearch,
	}
}

func (t *toolset) handleSearch(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Query string `json:"query"`
		Page  int    `json:"page"`
	}
	if err := decode(raw, &a); err != nil {
		return "", err
	}
	res, err := t.API.SearchTickets(ctx, a.Query, a.Page)
	if err != nil {
		return "", toolError(err)
	}
	for i := range res.Tickets {
		t.prepare(&res.Tickets[i])
	}
	capNote := ""
	if res.Total > freshdesk.MaxSearchPages*freshdesk.SearchPageSize {
		capNote = fmt.Sprintf("%d tickets match but Freshdesk only lets you read the first %d. Narrow the query. ",
			res.Total, freshdesk.MaxSearchPages*freshdesk.SearchPageSize)
	}
	return fit(res.Tickets, t.MaxResultBytes, func(shown []freshdesk.Ticket, cut bool) any {
		return struct {
			Total     int                `json:"total_matches"`
			Page      int                `json:"page"`
			Count     int                `json:"count"`
			HasMore   bool               `json:"has_more"`
			Note      string             `json:"note,omitempty"`
			RateLimit *rateLimitInfo     `json:"rate_limit,omitempty"`
			Tickets   []freshdesk.Ticket `json:"tickets"`
		}{res.Total, res.Page, len(shown), res.HasMore, capNote + noteIf(cut), t.rateLimit(), shown}
	}), nil
}

// ---- freshdesk_list_conversations ------------------------------------------

func (t *toolset) listConversations() mcp.Tool {
	return mcp.Tool{
		Name:        "freshdesk_list_conversations",
		Description: "List the replies and internal notes on a ticket, oldest first. incoming=true means the customer wrote it; private=true means it is an internal note.",
		InputSchema: object(props{
			"ticket_id": ticketID,
			"page":      integer("Page number, starting at 1.", 1, 1000, 1),
			"per_page":  integer("Messages per page.", 1, 100, 20),
		}, "ticket_id"),
		Handler: t.handleConversations,
	}
}

func (t *toolset) handleConversations(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		TicketID int64 `json:"ticket_id"`
		Page     int   `json:"page"`
		PerPage  int   `json:"per_page"`
	}
	if err := decode(raw, &a); err != nil {
		return "", err
	}
	if a.PerPage == 0 {
		a.PerPage = 20
	}
	convs, err := t.API.ListConversations(ctx, a.TicketID, a.Page, a.PerPage)
	if err != nil {
		return "", toolError(err)
	}
	for i := range convs {
		t.prepareConversation(&convs[i])
	}
	return fit(convs, t.MaxResultBytes, func(shown []freshdesk.Conversation, cut bool) any {
		return struct {
			TicketID      int64                    `json:"ticket_id"`
			Count         int                      `json:"count"`
			HasMore       bool                     `json:"has_more"`
			Note          string                   `json:"note,omitempty"`
			RateLimit     *rateLimitInfo           `json:"rate_limit,omitempty"`
			Conversations []freshdesk.Conversation `json:"conversations"`
		}{a.TicketID, len(shown), len(convs) == a.PerPage, noteIf(cut), t.rateLimit(), shown}
	}), nil
}
