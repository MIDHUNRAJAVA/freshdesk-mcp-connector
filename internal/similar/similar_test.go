package similar

import (
	"reflect"
	"strings"
	"testing"
)

var corpus = []Doc{
	{ID: 1, Subject: "Refund not received for cancelled order", Body: "I cancelled my order last week and the refund has not arrived in my bank account"},
	{ID: 2, Subject: "Where is my refund?", Body: "Order was cancelled, refund still not credited to my account"},
	{ID: 3, Subject: "Settlement report missing", Body: "The weekly settlement report did not show up in the dashboard"},
	{ID: 4, Subject: "Cannot log in to dashboard", Body: "Password reset email never arrives so I cannot log in"},
	{ID: 5, Subject: "Duplicate charge on card", Body: "My card was charged twice for one order, please reverse one charge"},
}

func TestRankPutsTheRelatedTicketFirstAndExplainsWhy(t *testing.T) {
	got := Rank(corpus[0], corpus, 3, 0.05)
	if len(got) == 0 || got[0].ID != 2 {
		t.Fatalf("expected ticket 2 first, got %+v", got)
	}
	shared := strings.Join(got[0].SharedTerms, " ")
	if !strings.Contains(shared, "refund") {
		t.Fatalf("explanation should mention refund: %v", got[0].SharedTerms)
	}
	for _, m := range got {
		if m.ID == 1 {
			t.Fatal("the target must not match itself")
		}
		if m.Score <= 0 || m.Score > 1 {
			t.Fatalf("score out of range: %+v", m)
		}
	}
}

func TestUnrelatedTicketsScoreLowerThanRelatedOnes(t *testing.T) {
	got := Rank(corpus[0], corpus, 10, 0)
	scores := map[int64]float64{}
	for _, m := range got {
		scores[m.ID] = m.Score
	}
	if scores[2] <= scores[3] || scores[2] <= scores[5] {
		t.Fatalf("scores = %v", scores)
	}
}

func TestMinScoreAndLimit(t *testing.T) {
	if got := Rank(corpus[0], corpus, 10, 0.99); len(got) != 0 {
		t.Fatalf("nothing should pass a 0.99 threshold: %+v", got)
	}
	if got := Rank(corpus[0], corpus, 1, 0); len(got) != 1 {
		t.Fatalf("limit not applied: %+v", got)
	}
}

func TestDeterministic(t *testing.T) {
	a := Rank(corpus[3], corpus, 5, 0)
	b := Rank(corpus[3], corpus, 5, 0)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same input must give the same output")
	}
}

func TestTokens(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Hello, the Refund of 12345 is NOT here!", "refund"},
		{"a1b2 ok", "a1b2"},
		{"", ""},
		{"e-mail: a***@***.com", "mail"},
	}
	for _, tc := range tests {
		if got := strings.Join(tokens(tc.in), " "); got != tc.want {
			t.Errorf("tokens(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEmptyInputs(t *testing.T) {
	if got := Rank(Doc{ID: 1}, nil, 5, 0); len(got) != 0 {
		t.Fatal("no candidates, no matches")
	}
	if got := Rank(Doc{ID: 1}, []Doc{{ID: 2, Subject: "refund"}}, 5, 0); len(got) != 0 {
		t.Fatalf("empty target matches nothing: %+v", got)
	}
}
