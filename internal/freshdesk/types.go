package freshdesk

// Only the fields an agent needs are decoded. Attachments, HTML bodies and
// custom fields are deliberately left out: less data means less to leak.

type Requester struct {
	ID     int64  `json:"id,omitempty"`
	Name   string `json:"name,omitempty"`
	Email  string `json:"email,omitempty"`
	Phone  string `json:"phone,omitempty"`
	Mobile string `json:"mobile,omitempty"`
}

type Ticket struct {
	ID              int64          `json:"id"`
	Subject         string         `json:"subject"`
	DescriptionText string         `json:"description_text,omitempty"`
	Status          int            `json:"status"`
	StatusLabel     string         `json:"status_label,omitempty"`
	Priority        int            `json:"priority"`
	PriorityLabel   string         `json:"priority_label,omitempty"`
	Type            string         `json:"type,omitempty"`
	RequesterID     int64          `json:"requester_id,omitempty"`
	ResponderID     int64          `json:"responder_id,omitempty"`
	GroupID         int64          `json:"group_id,omitempty"`
	CompanyID       int64          `json:"company_id,omitempty"`
	Tags            []string       `json:"tags,omitempty"`
	CreatedAt       string         `json:"created_at,omitempty"`
	UpdatedAt       string         `json:"updated_at,omitempty"`
	DueBy           string         `json:"due_by,omitempty"`
	Requester       *Requester     `json:"requester,omitempty"`
	Conversations   []Conversation `json:"conversations,omitempty"`
	// SecurityNotes is filled by the connector (never by Freshdesk): what the
	// shield found in the text. See internal/shield.
	SecurityNotes []string `json:"security_notes,omitempty"`
}

type Conversation struct {
	ID        int64    `json:"id"`
	TicketID  int64    `json:"ticket_id,omitempty"`
	UserID    int64    `json:"user_id,omitempty"`
	Incoming  bool     `json:"incoming"` // true = written by the customer
	Private   bool     `json:"private"`  // true = internal note
	BodyText  string   `json:"body_text,omitempty"`
	FromEmail string   `json:"from_email,omitempty"`
	ToEmails  []string `json:"to_emails,omitempty"`
	CcEmails  []string `json:"cc_emails,omitempty"`
	BccEmails []string `json:"bcc_emails,omitempty"`
	CreatedAt string   `json:"created_at,omitempty"`
	UpdatedAt string   `json:"updated_at,omitempty"`
	// SecurityNotes is filled by the connector: what the shield found in the text.
	SecurityNotes []string `json:"security_notes,omitempty"`
}

// Freshdesk status and priority codes, from its public API documentation.
const (
	StatusOpen     = 2
	StatusPending  = 3
	StatusResolved = 4
	StatusClosed   = 5
)

var statusLabels = map[int]string{2: "open", 3: "pending", 4: "resolved", 5: "closed"}
var priorityLabels = map[int]string{1: "low", 2: "medium", 3: "high", 4: "urgent"}

// StatusCode converts a label such as "open" to Freshdesk's number.
func StatusCode(label string) (int, bool) {
	for code, l := range statusLabels {
		if l == label {
			return code, true
		}
	}
	return 0, false
}

func (t *Ticket) addLabels() {
	t.StatusLabel = statusLabels[t.Status]
	t.PriorityLabel = priorityLabels[t.Priority]
}

// TicketPage is one page of a ticket list.
type TicketPage struct {
	Tickets []Ticket
	Page    int
	PerPage int
	// HasMore is true when the page was full, so another page probably exists.
	HasMore bool
}

// SearchResult is one page of search results.
type SearchResult struct {
	Total   int
	Tickets []Ticket
	Page    int
	HasMore bool
}

// RateLimit is what Freshdesk last told us about our allowance.
type RateLimit struct {
	Total     int
	Remaining int
}
