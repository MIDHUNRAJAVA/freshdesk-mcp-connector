// Package mask hides personal details (email addresses, phone numbers and,
// optionally, names) in ticket data before the agent sees it.
//
// It errs on the side of hiding too much. A long order number may be masked
// as if it were a phone number; that is the safer mistake.
package mask

import (
	"regexp"
	"strings"
	"unicode"

	"freshdesk-mcp-connector/internal/freshdesk"
)

// Masker is a value type: copy it freely. The zero value masks nothing, so
// callers must opt out explicitly (see config, where masking is on by default).
type Masker struct {
	Enabled bool // email and phone masking
	Names   bool // also reduce person names to initials
}

var (
	emailRe = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)+`)
	// A run that starts with a digit or +, contains digits and common
	// separators, and ends with a digit.
	phoneRe = regexp.MustCompile(`[+(]?\d[\d\s().\-]{7,}\d`)
	dateRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)
)

// String masks emails and phone numbers inside free text.
func (m Masker) String(s string) string {
	if !m.Enabled || s == "" {
		return s
	}
	s = emailRe.ReplaceAllStringFunc(s, maskEmail)
	return phoneRe.ReplaceAllStringFunc(s, maskPhoneInText)
}

// maskEmail keeps the first letter and the domain ending, so the agent can
// still tell two people apart without learning who they are.
func maskEmail(e string) string {
	at := strings.LastIndex(e, "@")
	local, domain := e[:at], e[at+1:]
	tld := domain[strings.LastIndex(domain, ".")+1:]
	return string([]rune(local)[:1]) + "***@***." + tld
}

func maskPhoneInText(p string) string {
	digits := digitsOf(p)
	// 10 to 15 digits is the range of real phone numbers. Dates such as
	// 2026-01-02 10:30 are left alone.
	if len(digits) < 10 || len(digits) > 15 || dateRe.MatchString(p) {
		return p
	}
	return phoneTail(digits)
}

func phoneTail(digits string) string {
	return "[phone ending " + digits[len(digits)-2:] + "]"
}

func digitsOf(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// phoneField masks a value we already know is a phone number.
func (m Masker) phoneField(s string) string {
	if !m.Enabled || s == "" {
		return s
	}
	d := digitsOf(s)
	if len(d) < 2 {
		return "[phone masked]"
	}
	return phoneTail(d)
}

func (m Masker) emailField(s string) string {
	if !m.Enabled || !strings.Contains(s, "@") {
		return m.String(s)
	}
	return maskEmail(s)
}

func (m Masker) name(s string) string {
	if !m.Enabled || !m.Names || s == "" {
		return s
	}
	var out []string
	for _, w := range strings.Fields(s) {
		out = append(out, string([]rune(w)[:1])+".")
	}
	return strings.Join(out, " ")
}

func (m Masker) emails(list []string) []string {
	if !m.Enabled || list == nil {
		return list
	}
	out := make([]string, len(list))
	for i, e := range list {
		out[i] = m.emailField(e)
	}
	return out
}

// Ticket masks t in place, including its requester and embedded replies.
func (m Masker) Ticket(t *freshdesk.Ticket) {
	if !m.Enabled || t == nil {
		return
	}
	t.Subject = m.String(t.Subject)
	t.DescriptionText = m.String(t.DescriptionText)
	for i, tag := range t.Tags {
		t.Tags[i] = m.String(tag)
	}
	if r := t.Requester; r != nil {
		r.Name = m.name(r.Name)
		r.Email = m.emailField(r.Email)
		r.Phone = m.phoneField(r.Phone)
		r.Mobile = m.phoneField(r.Mobile)
	}
	for i := range t.Conversations {
		m.Conversation(&t.Conversations[i])
	}
}

// Conversation masks c in place.
func (m Masker) Conversation(c *freshdesk.Conversation) {
	if !m.Enabled || c == nil {
		return
	}
	c.BodyText = m.String(c.BodyText)
	c.FromEmail = m.emailField(c.FromEmail)
	c.ToEmails = m.emails(c.ToEmails)
	c.CcEmails = m.emails(c.CcEmails)
	c.BccEmails = m.emails(c.BccEmails)
}
