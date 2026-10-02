package freshdesk

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Limits from the Freshdesk API documentation.
const (
	MaxPerPage        = 100 // list endpoints
	SearchPageSize    = 30  // search always returns at most 30 per page
	MaxSearchPages    = 10  // search will not go past page 10 (300 results)
	maxSearchQueryLen = 500
)

var (
	validFilters  = set("new_and_my_open", "watching", "spam", "deleted")
	validOrderBy  = set("created_at", "due_by", "updated_at", "status")
	validOrderDir = set("asc", "desc")
	validIncludes = set("requester", "stats", "company", "description")
)

func set(v ...string) map[string]bool {
	m := map[string]bool{}
	for _, s := range v {
		m[s] = true
	}
	return m
}

// ListOptions are the filters for ListTickets. Zero values mean "not set".
type ListOptions struct {
	Page         int // default 1
	PerPage      int // default 30, max 100
	UpdatedSince time.Time
	Filter       string // predefined Freshdesk filter, see validFilters
	OrderBy      string
	OrderType    string
	Include      []string // requester, stats, company, description
	// Status keeps only tickets with this status. Freshdesk's list endpoint
	// cannot filter by status, so this is applied to the returned page.
	// Use SearchTickets to filter across all pages.
	Status int
}

func (o ListOptions) query() (url.Values, int, int, error) {
	page, per := o.Page, o.PerPage
	if page == 0 {
		page = 1
	}
	if per == 0 {
		per = 30
	}
	if page < 1 {
		return nil, 0, 0, invalid("page must be 1 or more")
	}
	if per < 1 || per > MaxPerPage {
		return nil, 0, 0, invalid("per_page must be between 1 and %d", MaxPerPage)
	}
	q := url.Values{"page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(per)}}
	if !o.UpdatedSince.IsZero() {
		q.Set("updated_since", o.UpdatedSince.UTC().Format(time.RFC3339))
	}
	for name, v := range map[string]struct {
		val   string
		valid map[string]bool
	}{
		"filter":     {o.Filter, validFilters},
		"order_by":   {o.OrderBy, validOrderBy},
		"order_type": {o.OrderType, validOrderDir},
	} {
		if v.val == "" {
			continue
		}
		if !v.valid[v.val] {
			return nil, 0, 0, invalid("%s %q is not allowed", name, v.val)
		}
		q.Set(name, v.val)
	}
	for _, inc := range o.Include {
		if !validIncludes[inc] {
			return nil, 0, 0, invalid("include %q is not allowed", inc)
		}
	}
	if len(o.Include) > 0 {
		q.Set("include", strings.Join(o.Include, ","))
	}
	if o.Status != 0 && statusLabels[o.Status] == "" {
		return nil, 0, 0, invalid("status must be 2 (open), 3 (pending), 4 (resolved) or 5 (closed)")
	}
	return q, page, per, nil
}

// ListTickets returns one page of tickets.
func (c *Client) ListTickets(ctx context.Context, o ListOptions) (*TicketPage, error) {
	q, page, per, err := o.query()
	if err != nil {
		return nil, err
	}
	var tickets []Ticket
	if err := c.getJSON(ctx, "/tickets", q, &tickets); err != nil {
		return nil, err
	}
	out := &TicketPage{Page: page, PerPage: per, HasMore: len(tickets) == per}
	for i := range tickets {
		tickets[i].addLabels()
		if o.Status == 0 || tickets[i].Status == o.Status {
			out.Tickets = append(out.Tickets, tickets[i])
		}
	}
	return out, nil
}

// GetTicket returns one ticket with its requester. With conversations, the
// first batch of replies is embedded too (Freshdesk caps how many; use
// ListConversations for the whole thread).
func (c *Client) GetTicket(ctx context.Context, id int64, withConversations bool) (*Ticket, error) {
	if id <= 0 {
		return nil, invalid("ticket id must be a positive number")
	}
	include := "requester"
	if withConversations {
		include += ",conversations"
	}
	var t Ticket
	if err := c.getJSON(ctx, fmt.Sprintf("/tickets/%d", id), url.Values{"include": {include}}, &t); err != nil {
		return nil, err
	}
	t.addLabels()
	return &t, nil
}

// SearchTickets runs a Freshdesk search query such as
// "status:2 AND priority:3" or "tag:'refund'". Results come 30 per page and
// Freshdesk allows at most 10 pages, so at most 300 tickets are reachable
// from one query. Narrow the query rather than paging deeper.
func (c *Client) SearchTickets(ctx context.Context, query string, page int) (*SearchResult, error) {
	query = strings.TrimSpace(query)
	query = strings.Trim(query, `"`)
	if page == 0 {
		page = 1
	}
	switch {
	case query == "":
		return nil, invalid("query must not be empty")
	case len(query) > maxSearchQueryLen:
		return nil, invalid("query is longer than %d characters", maxSearchQueryLen)
	case strings.Contains(query, `"`):
		return nil, invalid(`use single quotes inside the query, for example tag:'refund'`)
	case page < 1 || page > MaxSearchPages:
		return nil, invalid("search page must be between 1 and %d (Freshdesk limit)", MaxSearchPages)
	}
	// Freshdesk requires the whole query to be wrapped in double quotes.
	q := url.Values{"query": {`"` + query + `"`}, "page": {strconv.Itoa(page)}}
	var raw struct {
		Total   int      `json:"total"`
		Results []Ticket `json:"results"`
	}
	if err := c.getJSON(ctx, "/search/tickets", q, &raw); err != nil {
		return nil, err
	}
	for i := range raw.Results {
		raw.Results[i].addLabels()
	}
	return &SearchResult{
		Total:   raw.Total,
		Tickets: raw.Results,
		Page:    page,
		HasMore: page < MaxSearchPages && page*SearchPageSize < raw.Total,
	}, nil
}

// ListConversations returns the replies and notes on a ticket, oldest first.
func (c *Client) ListConversations(ctx context.Context, ticketID int64, page, perPage int) ([]Conversation, error) {
	if ticketID <= 0 {
		return nil, invalid("ticket id must be a positive number")
	}
	if page == 0 {
		page = 1
	}
	if perPage == 0 {
		perPage = 30
	}
	if page < 1 || perPage < 1 || perPage > MaxPerPage {
		return nil, invalid("page must be 1 or more and per_page between 1 and %d", MaxPerPage)
	}
	q := url.Values{"page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(perPage)}}
	var convs []Conversation
	if err := c.getJSON(ctx, fmt.Sprintf("/tickets/%d/conversations", ticketID), q, &convs); err != nil {
		return nil, err
	}
	return convs, nil
}
