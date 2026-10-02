package mask

import (
	"strings"
	"testing"

	"freshdesk-mcp-connector/internal/freshdesk"
)

var on = Masker{Enabled: true}

func TestStringMasksEmailsAndPhones(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"email", "write to asha.verma@example.com please", "write to a***@***.com please"},
		{"two emails", "a@b.io and carl+x@mail.example.org", "a***@***.io and c***@***.org"},
		{"indian mobile", "call +91 98765 43210 now", "call [phone ending 10] now"},
		{"us style", "reach me at (555) 010-4477.", "reach me at [phone ending 77]."},
		{"dashes", "555-010-4477", "[phone ending 77]"},
		{"date is kept", "created 2026-01-02 10:30", "created 2026-01-02 10:30"},
		{"short number is kept", "order 12345 and 1234567", "order 12345 and 1234567"},
		{"nothing to mask", "my card was charged twice", "my card was charged twice"},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := on.String(tc.in); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestDisabledMasksNothing(t *testing.T) {
	in := "asha@example.com +91 98765 43210"
	if got := (Masker{}).String(in); got != in {
		t.Fatalf("disabled masker changed the text: %q", got)
	}
	tk := &freshdesk.Ticket{Subject: in}
	Masker{}.Ticket(tk)
	if tk.Subject != in {
		t.Fatal("disabled masker changed the ticket")
	}
}

func TestTicketMasksEveryPersonalField(t *testing.T) {
	tk := &freshdesk.Ticket{
		Subject:         "Refund for asha@example.com",
		DescriptionText: "My number is +91 98765 43210",
		Tags:            []string{"vip:asha@example.com"},
		Requester:       &freshdesk.Requester{Name: "Asha Verma", Email: "asha@example.com", Phone: "12345", Mobile: "+1 555 010 4477"},
		Conversations: []freshdesk.Conversation{{
			BodyText: "Thanks, email me at asha@example.com", FromEmail: "asha@example.com",
			ToEmails: []string{"support@shop.example.com"}, CcEmails: []string{"boss@example.com"},
		}},
	}
	on.Ticket(tk)
	b := strings.Join([]string{tk.Subject, tk.DescriptionText, tk.Tags[0], tk.Requester.Email, tk.Requester.Phone,
		tk.Requester.Mobile, tk.Conversations[0].BodyText, tk.Conversations[0].FromEmail,
		tk.Conversations[0].ToEmails[0], tk.Conversations[0].CcEmails[0]}, "|")
	for _, leak := range []string{"asha@", "example.com", "98765", "43210", "12345", "4477", "support@"} {
		if strings.Contains(b, leak) {
			t.Errorf("leaked %q in %s", leak, b)
		}
	}
	if tk.Requester.Name != "Asha Verma" {
		t.Errorf("names are kept unless Names is on, got %q", tk.Requester.Name)
	}
}

func TestNamesReducedToInitialsWhenEnabled(t *testing.T) {
	m := Masker{Enabled: true, Names: true}
	tk := &freshdesk.Ticket{Requester: &freshdesk.Requester{Name: "Asha Kumari Verma"}}
	m.Ticket(tk)
	if tk.Requester.Name != "A. K. V." {
		t.Fatalf("got %q", tk.Requester.Name)
	}
}

func TestNilSafe(t *testing.T) {
	on.Ticket(nil)
	on.Conversation(nil)
	on.Ticket(&freshdesk.Ticket{}) // no requester, no panic
}
