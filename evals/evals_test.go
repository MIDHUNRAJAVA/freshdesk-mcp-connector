package main

import (
	"context"
	"testing"
)

func TestAllCasesPass(t *testing.T) {
	results, err := runAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 10 {
		t.Fatalf("expected 10 cases, got %d", len(results))
	}
	for _, r := range results {
		if !r.ToolsOK || !r.FactsOK {
			t.Errorf("%q failed: %v\nanswer: %s", r.Case.Question, r.Problems, r.Answer)
		}
	}
	if passed := report(results); passed != 10 {
		t.Errorf("report counted %d passes", passed)
	}
}

// The grader must be able to fail, or a pass means nothing.
func TestGraderCatchesProblems(t *testing.T) {
	c := Case{Question: "q", WantTools: []string{"a"}, WantFacts: []string{"hello"}, WantAbsent: []string{"secret"}}
	cases := []struct {
		name    string
		answer  string
		used    []string
		toolsOK bool
		factsOK bool
	}{
		{"good", "Hello there", []string{"a"}, true, true},
		{"wrong tool", "Hello", []string{"b"}, false, true},
		{"extra tool", "Hello", []string{"a", "a"}, false, true},
		{"missing fact", "Goodbye", []string{"a"}, true, false},
		{"leaked value", "Hello, the SECRET is 1", []string{"a"}, true, false},
	}
	for _, tc := range cases {
		r := grade(c, tc.answer, tc.used)
		if r.ToolsOK != tc.toolsOK || r.FactsOK != tc.factsOK {
			t.Errorf("%s: got tools=%v facts=%v", tc.name, r.ToolsOK, r.FactsOK)
		}
	}
	if r := grade(Case{WantTools: []string{}}, "", nil); !r.ToolsOK {
		t.Error("no tools wanted and none used should pass")
	}
}
