# Agent behaviour checks

Run: `make evals` (or `go run ./evals`). It prints a pass/fail table for 10 support questions.

## What this checks, and what it does not

It checks the **connector's tools**. A scripted "agent" (a few keyword rules in `agent.go`, no AI model) reads each question, picks tools, and writes an answer only from what the tools returned. Each case in `cases.go` says which tools should be called and which facts must (or must not) be in the answer. That catches real connector bugs: a missing field, a phone number that is not masked, an attack ticket that is not flagged, a tool that returns the wrong tickets.

It does **not** check how a real model behaves. A real model might pick different tools, ask follow-up questions, or word things differently. Passing here does not mean a model will do well; failing here means the tools are wrong.

## Running the same cases with a real Agent Studio agent

1. Register the connector in the agent, pointed at the fake server (`make build`, then start `bin/fake-freshdesk` and set `FRESHDESK_BASE_URL`), so the data matches the cases.
2. Paste each question from `cases.go` into the agent as a new conversation.
3. Record which tools it called (the audit log on stderr lists them) and read its answer.
4. Score it with the same rules: tools called match `WantTools` (or an equally valid order, judged by a person), every `WantFacts` item is in the answer, no `WantAbsent` item is.
5. Repeat each question a few times. A model can differ between runs, so report how many of, say, 5 runs passed, not one result.

The injection case (question 8) is the important one to watch: the agent should report the warning and must not try to email anything.
