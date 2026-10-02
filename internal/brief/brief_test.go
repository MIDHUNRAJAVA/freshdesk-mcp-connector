package brief

import (
	"strings"
	"testing"
	"time"

	"freshdesk-mcp-connector/internal/freshdesk"
)

var now = time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)

func ago(h float64) string {
	return now.Add(-time.Duration(h * float64(time.Hour))).Format(time.RFC3339)
}
func in(h float64) string { return now.Add(time.Duration(h * float64(time.Hour))).Format(time.RFC3339) }

func ticket(status int, created, due string) freshdesk.Ticket {
	t := freshdesk.Ticket{ID: 1, Subject: "Help", DescriptionText: "Where is my order?", Status: status,
		Priority: 2, CreatedAt: created, DueBy: due, Requester: &freshdesk.Requester{Name: "Asha V."}}
	t.StatusLabel = map[int]string{2: "open", 3: "pending", 4: "resolved", 5: "closed"}[status]
	t.PriorityLabel = "medium"
	return t
}

func conv(at string, incoming, private bool, text string) freshdesk.Conversation {
	return freshdesk.Conversation{CreatedAt: at, Incoming: incoming, Private: private, BodyText: text}
}

func TestNextStepRules(t *testing.T) {
	tests := []struct {
		name        string
		ticket      freshdesk.Ticket
		convs       []freshdesk.Conversation
		wantWaiting string
		wantStep    string
		wantOverdue bool
	}{
		{
			name:        "customer waiting, not overdue",
			ticket:      ticket(2, ago(5), in(10)),
			wantWaiting: "agent", wantStep: "Reply to the customer",
		},
		{
			name:        "customer waiting and overdue",
			ticket:      ticket(2, ago(30), ago(6)),
			wantWaiting: "agent", wantStep: "Reply now", wantOverdue: true,
		},
		{
			name:        "we replied recently",
			ticket:      ticket(3, ago(30), in(5)),
			convs:       []freshdesk.Conversation{conv(ago(2), false, false, "On it")},
			wantWaiting: "customer", wantStep: "Wait for the customer",
		},
		{
			name:        "we replied days ago",
			ticket:      ticket(3, ago(200), in(5)),
			convs:       []freshdesk.Conversation{conv(ago(100), false, false, "Any update?")},
			wantWaiting: "customer", wantStep: "friendly follow-up",
		},
		{
			name:        "overdue but waiting on customer",
			ticket:      ticket(3, ago(100), ago(10)),
			convs:       []freshdesk.Conversation{conv(ago(20), false, false, "Please confirm")},
			wantWaiting: "customer", wantStep: "waiting on the customer", wantOverdue: true,
		},
		{
			name:        "resolved ticket is never overdue",
			ticket:      ticket(4, ago(100), ago(50)),
			wantWaiting: "nobody", wantStep: "No action needed",
		},
		{
			name:        "customer wrote back after our reply",
			ticket:      ticket(2, ago(50), in(5)),
			convs:       []freshdesk.Conversation{conv(ago(40), false, false, "Try X"), conv(ago(3), true, false, "Still broken")},
			wantWaiting: "agent", wantStep: "Reply to the customer",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := Build(tc.ticket, tc.convs, true, now)
			if b.WaitingOn != tc.wantWaiting {
				t.Errorf("waiting_on = %q, want %q", b.WaitingOn, tc.wantWaiting)
			}
			if !strings.Contains(b.SuggestedNextStep, tc.wantStep) {
				t.Errorf("step = %q, want it to contain %q", b.SuggestedNextStep, tc.wantStep)
			}
			if b.Overdue != tc.wantOverdue {
				t.Errorf("overdue = %v, want %v", b.Overdue, tc.wantOverdue)
			}
		})
	}
}

func TestCountsHoursAndInternalNotes(t *testing.T) {
	tk := ticket(2, ago(48), in(24))
	convs := []freshdesk.Conversation{
		conv(ago(40), false, false, "Looking"),
		conv(ago(39), false, true, "internal: check logs"),
		conv(ago(6), true, false, "Any news?\n\n   please"),
	}
	b := Build(tk, convs, false, now)
	if b.CustomerMessages != 2 || b.AgentReplies != 1 || b.InternalNotes != 1 {
		t.Fatalf("counts wrong: %+v", b)
	}
	if b.WaitingHours != 6 || b.AgeHours != 48 || b.DueInHours != 24 {
		t.Fatalf("hours wrong: waiting=%v age=%v due=%v", b.WaitingHours, b.AgeHours, b.DueInHours)
	}
	if b.LastCustomerMessage != "Any news? please" {
		t.Fatalf("excerpt = %q", b.LastCustomerMessage)
	}
	if b.ThreadComplete {
		t.Fatal("complete flag should pass through")
	}
}

func TestSignals(t *testing.T) {
	tk := ticket(2, ago(10), in(5))
	tk.Priority, tk.PriorityLabel = 4, "urgent"
	tk.Subject = "Refund please"
	convs := []freshdesk.Conversation{conv(ago(5), true, false, "I was charged twice"), conv(ago(2), true, false, "Hello??")}
	b := Build(tk, convs, true, now)
	joined := strings.Join(b.Signals, "; ")
	for _, want := range []string{"priority is urgent", "3 times in a row", "refund", "duplicate charge"} {
		if !strings.Contains(joined, want) {
			t.Errorf("signals %q missing %q", joined, want)
		}
	}
}

func TestExcerptIsBounded(t *testing.T) {
	tk := ticket(2, ago(1), "")
	tk.DescriptionText = strings.Repeat("word ", 200)
	b := Build(tk, nil, true, now)
	if len([]rune(b.LastCustomerMessage)) > excerptChars+3 {
		t.Fatalf("excerpt too long: %d", len(b.LastCustomerMessage))
	}
}

func TestMissingTimestampsDoNotPanic(t *testing.T) {
	b := Build(freshdesk.Ticket{ID: 3, Status: 2}, []freshdesk.Conversation{{Incoming: true}}, true, now)
	if b.WaitingOn != "agent" || b.SuggestedNextStep == "" {
		t.Fatalf("%+v", b)
	}
}
