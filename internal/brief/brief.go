// Package brief turns a ticket and its replies into a short summary using
// plain rules. No AI service is involved, so the same input always gives the
// same output and nothing leaves the machine.
package brief

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"freshdesk-mcp-connector/internal/freshdesk"
)

// Brief is the structured summary.
type Brief struct {
	TicketID  int64  `json:"ticket_id"`
	Subject   string `json:"subject"`
	Status    string `json:"status"`
	Priority  string `json:"priority"`
	Requester string `json:"requester,omitempty"`

	// WaitingOn is "agent" (customer is waiting for us), "customer" (we are
	// waiting for them) or "nobody" (ticket is resolved or closed).
	WaitingOn    string  `json:"waiting_on"`
	WaitingHours float64 `json:"waiting_hours"` // since the last public message
	AgeHours     float64 `json:"age_hours"`     // since the ticket was created

	LastCustomerMessage string `json:"last_customer_message,omitempty"`
	CustomerMessages    int    `json:"customer_messages"`
	AgentReplies        int    `json:"agent_replies"`
	InternalNotes       int    `json:"internal_notes"`

	Overdue           bool     `json:"overdue"`
	DueInHours        float64  `json:"due_in_hours,omitempty"`     // positive: time left
	OverdueByHours    float64  `json:"overdue_by_hours,omitempty"` // positive: time past due
	Signals           []string `json:"signals,omitempty"`          // things worth noticing
	SuggestedNextStep string   `json:"suggested_next_step"`
	ThreadComplete    bool     `json:"thread_complete"` // false if some replies were not fetched
}

const excerptChars = 300

// followUpAfter is how long we wait on a customer before suggesting a nudge.
const followUpAfter = 72 * time.Hour

// Words that deserve a heads-up. Kept short and obvious on purpose.
var keywordSignals = []struct{ word, note string }{
	{"refund", "customer mentions a refund"},
	{"chargeback", "customer mentions a chargeback"},
	{"charged twice", "possible duplicate charge"},
	{"double charged", "possible duplicate charge"},
	{"not received", "customer says something has not arrived"},
	{"cancel", "customer mentions cancelling"},
	{"legal", "customer mentions legal action"},
	{"urgent", "customer calls it urgent"},
	{"still waiting", "customer is following up again"},
}

type message struct {
	at       time.Time
	incoming bool
	private  bool
	text     string
}

// Build summarises a ticket. complete says whether convs is the whole thread.
// Pass already-masked data: the brief repeats text from the ticket.
func Build(t freshdesk.Ticket, convs []freshdesk.Conversation, complete bool, now time.Time) Brief {
	b := Brief{
		TicketID: t.ID, Subject: t.Subject, Status: t.StatusLabel, Priority: t.PriorityLabel,
		ThreadComplete: complete,
	}
	if t.Requester != nil {
		b.Requester = t.Requester.Name
	}
	created, _ := parseTime(t.CreatedAt)
	if !created.IsZero() {
		b.AgeHours = hoursBetween(created, now)
	}

	msgs := thread(t, convs, created)
	last := countMessages(&b, msgs)
	b.WaitingOn, b.WaitingHours = waiting(t.Status, last, now)
	b.Overdue, b.DueInHours, b.OverdueByHours = dueState(t, now)
	b.Signals = signals(t, msgs)
	b.SuggestedNextStep = nextStep(b, last)
	return b
}

// thread puts the ticket's first message and every reply in time order.
func thread(t freshdesk.Ticket, convs []freshdesk.Conversation, created time.Time) []message {
	var msgs []message
	if t.DescriptionText != "" {
		msgs = append(msgs, message{at: created, incoming: true, text: t.DescriptionText})
	}
	for _, c := range convs {
		at, _ := parseTime(c.CreatedAt)
		msgs = append(msgs, message{at: at, incoming: c.Incoming, private: c.Private, text: c.BodyText})
	}
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].at.Before(msgs[j].at) })
	return msgs
}

// countMessages fills the counters and returns the last public message.
func countMessages(b *Brief, msgs []message) *message {
	var last *message
	for i := range msgs {
		m := &msgs[i]
		switch {
		case m.private:
			b.InternalNotes++
			continue
		case m.incoming:
			b.CustomerMessages++
			b.LastCustomerMessage = excerpt(m.text)
		default:
			b.AgentReplies++
		}
		last = m
	}
	return last
}

func waiting(status int, last *message, now time.Time) (string, float64) {
	if status == freshdesk.StatusResolved || status == freshdesk.StatusClosed {
		return "nobody", 0
	}
	if last == nil {
		return "agent", 0
	}
	who := "customer"
	if last.incoming {
		who = "agent"
	}
	return who, hoursBetween(last.at, now)
}

func dueState(t freshdesk.Ticket, now time.Time) (overdue bool, dueIn, overdueBy float64) {
	due, ok := parseTime(t.DueBy)
	if !ok || t.Status == freshdesk.StatusResolved || t.Status == freshdesk.StatusClosed {
		return false, 0, 0
	}
	if now.After(due) {
		return true, 0, hoursBetween(due, now)
	}
	return false, hoursBetween(now, due), 0
}

func signals(t freshdesk.Ticket, msgs []message) []string {
	var out []string
	if t.Priority >= 3 {
		out = append(out, "priority is "+t.PriorityLabel)
	}
	if run := unansweredRun(msgs); run >= 2 {
		out = append(out, fmt.Sprintf("customer has written %d times in a row without a reply", run))
	}
	// Look at everything the customer said, not just the last message.
	parts := []string{t.Subject}
	for _, m := range msgs {
		if m.incoming && !m.private {
			parts = append(parts, m.text)
		}
	}
	text := strings.ToLower(strings.Join(parts, " "))
	for _, k := range keywordSignals {
		if strings.Contains(text, k.word) {
			out = append(out, k.note)
		}
	}
	return out
}

// unansweredRun counts customer messages since the last agent reply.
func unansweredRun(msgs []message) int {
	run := 0
	for _, m := range msgs {
		switch {
		case m.private:
		case m.incoming:
			run++
		default:
			run = 0
		}
	}
	return run
}

// nextStep applies the rules from most to least urgent; the first match wins.
func nextStep(b Brief, last *message) string {
	switch {
	case b.WaitingOn == "nobody":
		return "No action needed. Reopen only if the customer writes again."
	case b.Overdue && b.WaitingOn == "agent":
		return fmt.Sprintf("Reply now. The ticket is %s past its due time and the customer is waiting.", human(b.OverdueByHours))
	case b.Overdue:
		return fmt.Sprintf("Check the ticket. It is %s past its due time while waiting on the customer; consider a follow-up or closing it.", human(b.OverdueByHours))
	case b.WaitingOn == "agent":
		return fmt.Sprintf("Reply to the customer. Their last message is %s old.", human(b.WaitingHours))
	case b.WaitingOn == "customer" && time.Duration(b.WaitingHours*float64(time.Hour)) >= followUpAfter:
		return fmt.Sprintf("Send a friendly follow-up. The customer has not replied for %s.", human(b.WaitingHours))
	default:
		return "Wait for the customer to reply."
	}
}

func human(hours float64) string {
	if hours < 1 {
		return "under an hour"
	}
	if hours < 48 {
		return fmt.Sprintf("%.0f hours", hours)
	}
	return fmt.Sprintf("%.0f days", hours/24)
}

func excerpt(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= excerptChars {
		return s
	}
	return string(r[:excerptChars]) + "..."
}

func hoursBetween(from, to time.Time) float64 {
	h := to.Sub(from).Hours()
	if h < 0 {
		h = 0
	}
	return float64(int(h*10+0.5)) / 10 // one decimal place
}

func parseTime(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, s)
	return t, err == nil
}
