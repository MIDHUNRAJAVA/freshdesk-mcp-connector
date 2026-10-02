// Package fakefd is a stand-in for Freshdesk's API with invented tickets.
// It exists so the connector can be demoed and tested without an account or
// credentials, and it can misbehave on purpose (rate limits, server errors,
// an outage) to show how the connector copes.
package fakefd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"freshdesk-mcp-connector/internal/freshdesk"
)

// Options controls the fake's behaviour.
type Options struct {
	APIKey string // the only key accepted; default "demo-key"

	// Misbehaviour switches. All zero means a well-behaved server.
	Every429       int           // every Nth request answers 429 with Retry-After: 1
	Every500       int           // every Nth request answers 500
	Outage         time.Duration // answer 500 to everything for this long after start
	LimitPerMinute int           // real fixed-window rate limit; 0 = none (headers still sent)

	Now func() time.Time // defaults to time.Now
}

type server struct {
	opts    Options
	started time.Time
	tickets []freshdesk.Ticket
	convs   map[int64][]freshdesk.Conversation

	mu          sync.Mutex
	count       int
	windowStart time.Time
	windowUsed  int
}

// New returns the fake API as an http.Handler serving /api/v2/....
func New(o Options) http.Handler {
	if o.APIKey == "" {
		o.APIKey = "demo-key"
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	s := &server{opts: o, started: o.Now()}
	s.tickets, s.convs = build(s.started)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/tickets", s.guard(s.list))
	mux.HandleFunc("GET /api/v2/tickets/{id}", s.guard(s.get))
	mux.HandleFunc("GET /api/v2/tickets/{id}/conversations", s.guard(s.conversations))
	mux.HandleFunc("GET /api/v2/search/tickets", s.guard(s.search))
	return mux
}

// guard applies authentication, then the deliberate misbehaviour, then the handler.
func (s *server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != s.opts.APIKey || pass != "X" {
			writeJSON(w, 401, map[string]string{"code": "invalid_credentials", "message": "You have to be logged in to perform this action."})
			return
		}
		if status, retryAfter, over := s.chaos(); over {
			if retryAfter > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			}
			writeJSON(w, status, map[string]string{"description": "simulated failure"})
			return
		}
		next(w, r)
	}
}

// chaos decides whether this request should fail on purpose.
func (s *server) chaos() (status, retryAfter int, fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count++
	now := s.opts.Now()
	switch {
	case s.opts.Outage > 0 && now.Sub(s.started) < s.opts.Outage:
		return 500, 0, true
	case s.opts.Every500 > 0 && s.count%s.opts.Every500 == 0:
		return 500, 0, true
	case s.opts.Every429 > 0 && s.count%s.opts.Every429 == 0:
		return 429, 1, true
	}
	if s.opts.LimitPerMinute > 0 {
		if now.Sub(s.windowStart) >= time.Minute {
			s.windowStart, s.windowUsed = now, 0
		}
		if s.windowUsed >= s.opts.LimitPerMinute {
			wait := int(time.Minute.Seconds() - now.Sub(s.windowStart).Seconds())
			return 429, max(wait, 1), true
		}
		s.windowUsed++
	}
	return 0, 0, false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Ratelimit-Total", "100")
	w.Header().Set("X-Ratelimit-Remaining", "97")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func intParam(r *http.Request, name string, def int) int {
	if v, err := strconv.Atoi(r.URL.Query().Get(name)); err == nil && v > 0 {
		return v
	}
	return def
}

// view copies a ticket the way the real API would shape it for this request.
func view(t freshdesk.Ticket, includes string, single bool) freshdesk.Ticket {
	if !single && !strings.Contains(includes, "description") {
		t.DescriptionText = "" // list and search omit the description unless asked
	}
	if !strings.Contains(includes, "requester") && !single {
		t.Requester = nil
	}
	return t
}

func (s *server) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var since time.Time
	if v := q.Get("updated_since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeJSON(w, 400, map[string]string{"description": "Validation failed: updated_since is not a valid date"})
			return
		}
		since = t
	} else {
		// The real API only returns the last 30 days by default.
		since = s.opts.Now().AddDate(0, 0, -30)
	}

	var matched []freshdesk.Ticket
	for _, t := range s.tickets {
		updated, _ := time.Parse(time.RFC3339, t.UpdatedAt)
		created, _ := time.Parse(time.RFC3339, t.CreatedAt)
		recent := updated.After(since)
		if q.Get("updated_since") == "" {
			recent = created.After(since)
		}
		if recent && presetMatches(q.Get("filter"), t) {
			matched = append(matched, t)
		}
	}
	sortTickets(matched, q.Get("order_by"), q.Get("order_type"))

	per := min(intParam(r, "per_page", 30), freshdesk.MaxPerPage)
	page := intParam(r, "page", 1)
	out := []freshdesk.Ticket{}
	for _, t := range paginate(matched, page, per) {
		out = append(out, view(t, q.Get("include"), false))
	}
	writeJSON(w, 200, out)
}

func presetMatches(filter string, t freshdesk.Ticket) bool {
	switch filter {
	case "":
		return true
	case "new_and_my_open":
		return t.Status == freshdesk.StatusOpen
	}
	return false // spam, deleted, watching: none in the fake data
}

func sortTickets(ts []freshdesk.Ticket, by, dir string) {
	key := func(t freshdesk.Ticket) string {
		switch by {
		case "created_at":
			return t.CreatedAt
		case "due_by":
			return t.DueBy
		}
		return t.UpdatedAt
	}
	sort.SliceStable(ts, func(i, j int) bool {
		if dir == "asc" {
			return key(ts[i]) < key(ts[j])
		}
		return key(ts[i]) > key(ts[j])
	})
}

func paginate[T any](items []T, page, per int) []T {
	start := (page - 1) * per
	if start >= len(items) {
		return nil
	}
	return items[start:min(start+per, len(items))]
}

func (s *server) find(r *http.Request) (freshdesk.Ticket, bool) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	for _, t := range s.tickets {
		if t.ID == id {
			return t, true
		}
	}
	return freshdesk.Ticket{}, false
}

func notFound(w http.ResponseWriter) {
	writeJSON(w, 404, map[string]string{"code": "invalid_value", "description": "The ticket does not exist."})
}

func (s *server) get(w http.ResponseWriter, r *http.Request) {
	t, ok := s.find(r)
	if !ok {
		notFound(w)
		return
	}
	t = view(t, r.URL.Query().Get("include"), true)
	if strings.Contains(r.URL.Query().Get("include"), "conversations") {
		t.Conversations = paginate(s.convs[t.ID], 1, 10) // the real API embeds a limited number
	}
	writeJSON(w, 200, t)
}

func (s *server) conversations(w http.ResponseWriter, r *http.Request) {
	t, ok := s.find(r)
	if !ok {
		notFound(w)
		return
	}
	out := paginate(s.convs[t.ID], intParam(r, "page", 1), min(intParam(r, "per_page", 30), freshdesk.MaxPerPage))
	if out == nil {
		out = []freshdesk.Conversation{}
	}
	writeJSON(w, 200, out)
}

// ---- search ---------------------------------------------------------------

var searchFields = map[string]bool{"status": true, "priority": true, "tag": true}

var clauseRe = regexp.MustCompile(`^(\w+):('([^']*)'|\d+)$`)

func (s *server) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := q.Get("query")
	if len(query) < 2 || query[0] != '"' || query[len(query)-1] != '"' {
		writeJSON(w, 400, map[string]string{"description": "Validation failed: query must be wrapped in double quotes"})
		return
	}
	page := intParam(r, "page", 1)
	if page > freshdesk.MaxSearchPages {
		writeJSON(w, 400, map[string]string{"description": "Validation failed: page must be 10 or less"})
		return
	}
	var clauses [][2]string
	for _, part := range strings.Split(query[1:len(query)-1], " AND ") {
		m := clauseRe.FindStringSubmatch(strings.TrimSpace(part))
		if m == nil || !searchFields[m[1]] {
			writeJSON(w, 400, map[string]string{"description": "The fake server supports only: status:N, priority:N, tag:'x' joined with AND"})
			return
		}
		val := m[3]
		if val == "" {
			val = m[2]
		}
		clauses = append(clauses, [2]string{m[1], val})
	}

	var matched []freshdesk.Ticket
	for _, t := range s.tickets {
		if searchMatches(t, clauses) {
			matched = append(matched, t)
		}
	}
	sortTickets(matched, "updated_at", "desc")
	results := []freshdesk.Ticket{}
	for _, t := range paginate(matched, page, freshdesk.SearchPageSize) {
		results = append(results, view(t, "description,requester", false))
	}
	writeJSON(w, 200, map[string]any{"total": len(matched), "results": results})
}

func searchMatches(t freshdesk.Ticket, clauses [][2]string) bool {
	for _, c := range clauses {
		switch c[0] {
		case "status":
			if fmt.Sprint(t.Status) != c[1] {
				return false
			}
		case "priority":
			if fmt.Sprint(t.Priority) != c[1] {
				return false
			}
		case "tag":
			found := false
			for _, tag := range t.Tags {
				found = found || tag == c[1]
			}
			if !found {
				return false
			}
		}
	}
	return true
}
