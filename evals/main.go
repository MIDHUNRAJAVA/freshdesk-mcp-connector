// Command evals replays support questions against the connector's tools
// using a scripted agent (no AI model) and prints a pass/fail table.
//
// This checks the CONNECTOR: do the tools return what a good agent needs, is
// personal data masked, is an attack ticket flagged. It does not check how a
// real model behaves. See evals/README.md for how to run the
// same cases with a real Agent Studio agent.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"time"

	"freshdesk-mcp-connector/internal/fakefd"
	"freshdesk-mcp-connector/internal/freshdesk"
	"freshdesk-mcp-connector/internal/mask"
	"freshdesk-mcp-connector/internal/tools"
)

// Result is the outcome of one case.
type Result struct {
	Case     Case
	Answer   string
	Used     []string
	ToolsOK  bool
	FactsOK  bool
	Problems []string
}

// grade compares what the agent did with what the case expects.
func grade(c Case, answer string, used []string) Result {
	r := Result{Case: c, Answer: answer, Used: used}
	r.ToolsOK = slices.Equal(append([]string{}, used...), append([]string{}, c.WantTools...))
	if !r.ToolsOK {
		r.Problems = append(r.Problems, fmt.Sprintf("tools: want %v, got %v", c.WantTools, used))
	}
	la := strings.ToLower(answer)
	r.FactsOK = true
	for _, f := range c.WantFacts {
		if !strings.Contains(la, strings.ToLower(f)) {
			r.FactsOK = false
			r.Problems = append(r.Problems, fmt.Sprintf("answer is missing %q", f))
		}
	}
	for _, f := range c.WantAbsent {
		if strings.Contains(la, strings.ToLower(f)) {
			r.FactsOK = false
			r.Problems = append(r.Problems, fmt.Sprintf("answer must not contain %q", f))
		}
	}
	return r
}

// runAll starts the fake server, builds the real tools, and runs every case.
func runAll(ctx context.Context) ([]Result, error) {
	fake := httptest.NewServer(fakefd.New(fakefd.Options{}))
	defer fake.Close()
	client, err := freshdesk.NewClient(freshdesk.Config{APIKey: "demo-key", BaseURL: fake.URL + "/api/v2"})
	if err != nil {
		return nil, err
	}
	byName := map[string]func(context.Context, json.RawMessage) (string, error){}
	for _, t := range tools.New(tools.Deps{API: client, Mask: mask.Masker{Enabled: true}}) {
		byName[t.Name] = t.Handler
	}
	call := func(ctx context.Context, name, args string) (string, bool) {
		out, err := byName[name](ctx, json.RawMessage(args))
		if err != nil {
			return err.Error(), true
		}
		return out, false
	}
	var results []Result
	for _, c := range cases {
		answer, used := runAgent(ctx, c.Question, call, time.Now())
		results = append(results, grade(c, answer, used))
	}
	return results, nil
}

func mark(ok bool) string {
	if ok {
		return "pass"
	}
	return "FAIL"
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-3]) + "..."
	}
	return s
}

// report prints the table and scores, and returns how many cases passed.
func report(results []Result) int {
	fmt.Printf("%-3s %-52s %-6s %-6s %s\n", "#", "Question", "Tools", "Facts", "Result")
	passed, toolsOK, factsOK := 0, 0, 0
	for i, r := range results {
		ok := r.ToolsOK && r.FactsOK
		fmt.Printf("%-3d %-52s %-6s %-6s %s\n", i+1, clip(r.Case.Question, 52), mark(r.ToolsOK), mark(r.FactsOK), mark(ok))
		for _, p := range r.Problems {
			fmt.Printf("      - %s\n", p)
		}
		for _, b := range []struct {
			ok bool
			n  *int
		}{{ok, &passed}, {r.ToolsOK, &toolsOK}, {r.FactsOK, &factsOK}} {
			if b.ok {
				*b.n++
			}
		}
	}
	n := len(results)
	fmt.Printf("\nScores: tool choice %d/%d, answer facts %d/%d, cases fully passed %d/%d\n", toolsOK, n, factsOK, n, passed, n)
	fmt.Println("Reminder: this checks the connector's tools with a scripted agent, not a real model.")
	return passed
}

func main() {
	results, err := runAll(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "evals:", err)
		os.Exit(1)
	}
	if report(results) != len(results) {
		os.Exit(1)
	}
}
