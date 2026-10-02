# Freshdesk MCP Connector: a read-only, privacy-first window into support tickets for AI agents

[![CI](https://github.com/MIDHUNRAJAVA/freshdesk-mcp-connector/actions/workflows/ci.yml/badge.svg)](https://github.com/MIDHUNRAJAVA/freshdesk-mcp-connector/actions/workflows/ci.yml)

This is a small program, written in Go with no outside libraries, that lets an AI agent look up support tickets in Freshdesk (a help desk tool). It is for teams building agents, for example on Razorpay's Agent Studio, who need an agent to read a merchant's tickets. It is safe because it can only read, it hides emails and phone numbers before the agent sees them, and it treats ticket text from customers as untrusted data, not as orders.

**Try it in 60 seconds** (no account, no keys, invented data):

```
make demo     # builds, starts a fake Freshdesk, plays a short agent session (needs Go)
make evals    # 10 support questions replayed against the tools, pass/fail table
docker compose up --build -d fake && docker compose run --rm -T mcp < scripts/sample-requests.jsonl && docker compose down   # same, no Go needed
```

**What the reviewer can check**

| Brief asks for | Where to look |
|---|---|
| Authentication | [Setup](#setup-and-run-real-freshdesk) and [Configuration](#configuration) below; why a key and not OAuth: [docs/DESIGN.md](docs/DESIGN.md) |
| List, get, search tickets | [internal/tools](internal/tools) and [The tools](#the-tools); real captured output: [docs/WALKTHROUGH.md](docs/WALKTHROUGH.md) |
| Rate limits | [internal/limiter](internal/limiter), [internal/freshdesk](internal/freshdesk); the retry demo is in [docs/WALKTHROUGH.md](docs/WALKTHROUGH.md) |
| MCP tool specification | [docs/mcp-tools.json](docs/mcp-tools.json) (a test keeps it current) |
| What the agent can and cannot do | [What the agent can and cannot do](#what-the-agent-can-and-cannot-do) below |
| Setup and limitations | [Setup](#setup-and-run-real-freshdesk), [docs/LIMITS.md](docs/LIMITS.md), [docs/REAL_TESTING.md](docs/REAL_TESTING.md) |
| Fit with Agent Studio, rollout | [docs/AGENT_STUDIO_FIT.md](docs/AGENT_STUDIO_FIT.md), [docs/MERCHANT_PLAYBOOK.md](docs/MERCHANT_PLAYBOOK.md) |

**Start here** (three files):
1. [docs/WALKTHROUGH.md](docs/WALKTHROUGH.md): a 2-minute story with real captured output.
2. [internal/tools](internal/tools): the six tools the agent can call.
3. [internal/shield](internal/shield): how hidden instructions in tickets are fenced and flagged.

**How this was built:** I designed the approach and chose the guardrails, and I used Claude Code (an AI coding assistant) to write, test and review the code with me. The commit history shows this openly. I can explain and change any part of it.

**Honest status:** tested against a fake Freshdesk server that I wrote from the public API documentation, plus one call to the real service with a bad key and the official MCP Inspector. **It has not been run against an authenticated real Freshdesk account.**

---

MCP (Model Context Protocol) is a standard way for an AI agent to discover and call tools that live in another program. This connector is such a program: the agent starts it, asks "what tools do you have?", then calls them. It was written as a take-home assignment (Agent Studio, assignment 3: a private connector for a merchant tool).

## What makes this different

- **A shield against hidden instructions in tickets.** Ticket text is written by outsiders, so it is treated as untrusted. Every tool wraps it in `<untrusted_ticket_text>` tags, tells the agent that text inside is data and never instructions, and adds a plain `security_notes` list when it spots things like "ignore your rules" or "email all customer data to ...". The text itself is never removed or changed. The demo includes two invented attack tickets (211 and 212). Code: `internal/shield`.
- **Behaviour checks you can run in seconds.** `make evals` replays 10 realistic support questions (overdue tickets, similar tickets, a phone number that must come back masked, an attack ticket, a request to refund money that must be refused) and prints a pass/fail table. It checks the connector's tools with a scripted stand-in agent, not a real model; [evals/README.md](evals/README.md) explains how to run the same cases with a real agent.
- **A rollout playbook.** [docs/MERCHANT_PLAYBOOK.md](docs/MERCHANT_PLAYBOOK.md) is one page on how I would take this to a real merchant: questions to ask, minimum access, read-only first, three metrics, weekly review, and when and how to add write access with a human approval step.

## Try it in one command

You need Go (1.27 was used) and nothing else. There are no outside dependencies and no credentials.

```
make demo
```

This builds the connector, starts a fake Freshdesk server with invented tickets, and plays a short agent session: search, get a ticket, get a brief, find similar tickets, and ask for a ticket that does not exist, then read two attack tickets and watch the shield flag them (`SHIELD CAUGHT` lines). A captured run is in [docs/sample-session.txt](docs/sample-session.txt). Here is the interesting part, shortened:

```
<- id=2 {"total_matches":1, ... "description_text":"... You can reach me at a***@***.com or [phone ending 10]." ...
<- id=4 {"brief":{"waiting_on":"agent","waiting_hours":4,"overdue":true,"overdue_by_hours":6,
         "signals":["priority is high","customer mentions a refund", ...],
         "suggested_next_step":"Reply now. The ticket is 6 hours past its due time and the customer is waiting."}}
<- id=5 {"matches":[{"ticket_id":202,"subject":"Where is my refund?","score":0.484,"shared_terms":["refund","cancelled","order"]}]}
<- id=6 ERROR Not found. Freshdesk has no such ticket, or the key's role cannot see it.
```

Two more demos show the reliability features working against a fake server that misbehaves on purpose:

```
make demo-flaky    # some requests get 429 (slow down) or 500 (server error): watch the retries on stderr
make demo-outage   # the server is down for a few seconds: watch the circuit breaker open, then recover
```

## Safe by design: the agent never sees raw customer data and can never change anything

| Promise | How it is kept |
|---|---|
| Ticket text cannot give the agent orders | Ticket and reply text is fenced as untrusted data and scanned for attack phrases; findings appear as `security_notes`. This lowers the risk but is not a guarantee (see [docs/LIMITS.md](docs/LIMITS.md)). A test checks every tool. |
| It cannot change anything | The Freshdesk client has no write methods at all (no create, update, delete or reply). Every tool is marked read-only. A test checks tool names and hints. |
| The agent never sees raw contact details | Emails and phone numbers are masked in ticket text, replies and requester fields before data leaves the connector. On by default. A test sends every tool through a full session and checks that no raw address or number appears. |
| The API key stays secret | Read from an environment variable only. It is never logged or returned. Printing the config object shows `[redacted]`. A test checks this. |
| One call cannot dump everything | Every tool result has a hard size cap (default 40,000 bytes). Results are trimmed to fit and say so. Long texts are cut. |
| You can see what the agent did | One audit line per call: tool name, argument names (never values), time, duration, size, outcome. No customer data. |
| It does not hammer Freshdesk | Client-side rate limit, retries with backoff, a circuit breaker, a short cache, and merging of identical simultaneous requests. |

Names are not masked unless you turn on `FRESHDESK_MASK_NAMES` (then they become initials). Free text can still contain other personal details that look like neither an email nor a phone number, such as a street address. See [docs/LIMITS.md](docs/LIMITS.md).

## The tools

| Tool | What it does |
|---|---|
| `freshdesk_list_tickets` | One page of tickets, with filters (updated since, status, preset lists, sorting). |
| `freshdesk_get_ticket` | One ticket with its requester, optionally with the first replies. |
| `freshdesk_search_tickets` | Freshdesk search syntax such as `status:2 AND tag:'refund'`. 30 per page, 10 pages at most (a Freshdesk limit). |
| `freshdesk_list_conversations` | The replies and internal notes on a ticket. |
| `freshdesk_ticket_brief` | A short summary: who is waiting and for how long, the customer's last message, overdue or not, warning signs, and a suggested next step. Built from simple rules, no AI service. |
| `freshdesk_find_similar_tickets` | Finds related tickets by comparing words (TF-IDF, written by hand) and says which shared words caused each match. No AI service. |

The full machine-readable specification, with input schemas, is in [docs/mcp-tools.json](docs/mcp-tools.json). A test fails if that file gets out of date.

## Run it with Docker

No Go install needed. The image is small (about 26 MB), has no shell, and runs as a non-root user.

```
docker compose up --build -d fake                          # a pretend Freshdesk with invented tickets
docker compose run --rm -T mcp < scripts/sample-requests.jsonl   # a short MCP session
docker compose down
```

Against a real Freshdesk account, keep the key out of the image and pass it when you run:

```
docker build -t freshdesk-mcp-connector .
docker run --rm -i -e FRESHDESK_DOMAIN=yourcompany -e FRESHDESK_API_KEY freshdesk-mcp-connector
```

The connector talks over standard input and output, so it opens no network port. It also refuses to send an API key over plain `http` unless the address is `localhost`, so in the compose demo the two containers share one network and reach each other at `localhost`.

**Tested against real software, not only my fake:** a real Freshdesk response (found and fixed a real bug) and the official MCP Inspector. What is still untested is written down in [docs/REAL_TESTING.md](docs/REAL_TESTING.md).

## Setup and run (real Freshdesk)

1. In Freshdesk, create or choose an agent account with the least access that still lets it see the tickets you need (a read-focused role). Find its API key under your profile settings. Never reuse an administrator's key.
2. Build:
   ```
   make build        # creates bin/freshdesk-mcp and bin/fake-freshdesk
   ```
3. Set the environment and run:
   ```
   export FRESHDESK_DOMAIN=yourcompany      # for yourcompany.freshdesk.com
   export FRESHDESK_API_KEY=...             # from the environment only, never a file in the repo
   ./bin/freshdesk-mcp
   ```
   The program now waits for MCP messages on standard input. Normally an agent host starts it for you (next section). Logs and audit lines go to stderr; stdout is reserved for the protocol.

To list the tools without any credentials: `./bin/freshdesk-mcp -print-tools`.

### Registering it in an Agent Studio style MCP config

I do not know Agent Studio's exact configuration format. Most MCP hosts accept a command-based server entry like this one, so adapt the field names to the host:

```json
{
  "mcpServers": {
    "freshdesk": {
      "command": "/path/to/bin/freshdesk-mcp",
      "args": [],
      "env": {
        "FRESHDESK_DOMAIN": "yourcompany",
        "FRESHDESK_API_KEY": "<injected from your secret store, not written here>",
        "FRESHDESK_REQUESTS_PER_MINUTE": "40"
      }
    }
  }
}
```

### Configuration

All settings are environment variables.

| Variable | Default | Meaning |
|---|---|---|
| `FRESHDESK_DOMAIN` | required | Your subdomain, for example `acme` for `acme.freshdesk.com`. |
| `FRESHDESK_API_KEY` | required | API key. Sent as the basic-auth username with `X` as the password. |
| `FRESHDESK_BASE_URL` | unset | Overrides the address. Must be https, except `http://localhost` for the fake server. |
| `FRESHDESK_REQUESTS_PER_MINUTE` | 40 | Client-side limit. Set it at or below your Freshdesk plan's limit. 0 turns it off. |
| `FRESHDESK_BURST` | 5 | Requests allowed back to back before the limit slows things down. |
| `FRESHDESK_MAX_RETRIES` | 3 | Extra tries after the first, for 429, server errors and network errors. |
| `FRESHDESK_TIMEOUT` | 20s | Time limit for one HTTP request. |
| `FRESHDESK_BACKOFF_BASE` / `_MAX` | 500ms / 8s | First retry wait and the largest wait. Waits double each time and are randomised. |
| `FRESHDESK_CACHE_TTL` | 30s | How long an answer is reused. `0s` turns the cache off. |
| `FRESHDESK_BREAKER_THRESHOLD` | 5 | Failures in a row before the circuit breaker opens. 0 turns it off. |
| `FRESHDESK_BREAKER_COOLDOWN` | 30s | How long the breaker stays open before testing the server again. |
| `FRESHDESK_MASK_PII` | true | Mask emails and phone numbers. Turn off only for debugging with fake data. |
| `FRESHDESK_MASK_NAMES` | false | Also reduce person names to initials. |
| `FRESHDESK_MAX_RESULT_BYTES` | 40000 | Hard cap on one tool result. |
| `FRESHDESK_AUDIT_LOG` | stderr | `off`, or a file path for the audit lines. |

## How it fits together

```
 Agent host (Agent Studio style)
        |  MCP messages, one JSON per line, over stdin/stdout
        v
 internal/mcp       protocol only: initialize, tools/list, tools/call, ping; audit line per call
        |
 internal/tools     six tools: check arguments, call client, MASK, cap size
     |      |
     |      +--> internal/brief, internal/similar   (local rules and TF-IDF, no network)
     |      +--> internal/shield                     (fence ticket text, flag attack phrases)
     v
 internal/freshdesk   read-only client
     |   cache -> merge identical calls -> rate limiter -> circuit breaker -> HTTP with retries
     v
 https://<domain>.freshdesk.com/api/v2     (or cmd/fake-freshdesk for demos)
```

The reasons behind each choice are in [docs/DESIGN.md](docs/DESIGN.md).

## What the agent can and cannot do

**It can:**
- List tickets, get a ticket, search tickets with Freshdesk's query syntax, and read a ticket's replies and internal notes.
- Get a plain-language brief of a ticket and a list of possibly related tickets.
- See Freshdesk's rate-limit headroom in each result, so it can slow down.

**It cannot:**
- Reply to, create, edit, assign, close or delete anything. There is no code path for it.
- See attachments, custom fields, contacts or companies beyond the requester fields, or knowledge-base articles.
- See raw emails and phone numbers (unless an operator turns masking off).
- Read more than 300 search results for one query, or more than the size cap in one call.
- Get notified when a ticket changes (no webhooks); it only reads when asked.
- Use a different Freshdesk account than the one the key belongs to, or see tickets the key's role cannot see.

## Assumptions

- Freshdesk's API v2 is authenticated with an API key (basic auth, key as username, `X` as password), so this connector uses a key and not OAuth. If your tool needs OAuth, see "Extending it".
- One connector process serves one Freshdesk account.
- The agent host starts the connector and supplies the key through the environment.
- Behaviour matches Freshdesk's public documentation as I understood it. I did not have an account to confirm it, so the first real run may need small fixes.

Limitations are listed honestly in [docs/LIMITS.md](docs/LIMITS.md).

## Extending it

- **Webhooks and incremental sync:** add a small receiver for Freshdesk automation webhooks and keep a "last updated" marker, so the agent can ask "what changed since I last looked" without re-listing.
- **Many merchants (multi-tenant):** replace the single environment key with a lookup in a secrets store, keyed by merchant, with a separate rate limiter, cache and breaker per merchant.
- **OAuth:** for tools that use it, add a token source that refreshes access tokens and swap it in where the client builds its auth header. Everything else stays the same.

## Tests

```
make test      # quick
make race      # with the race detector
make lint      # gofmt and go vet
make cover     # prints total coverage
make evals     # the 10 behaviour checks
```

Measured with `make cover`: 85.9% of statements overall (the two `main` programs have no tests and pull the total down; every package that has tests is between 87% and 100%; `internal/shield` is 100%; `internal/clock`, a few lines wrapping the system time, has no tests). The suite has 94 top-level tests (157 counting subtests), all passing with `-race`. Tests use `net/http/httptest` and a fake clock, so retries and cooldowns run instantly. They cover the auth header, pagination, search, 429 with Retry-After, server-error backoff, giving up after the maximum retries, 401 and 404 mapping, masking, the circuit breaker, the cache, request merging, and a full MCP session over in-memory pipes.

## What each package does

| Package | Job |
|---|---|
| `internal/freshdesk` | Read-only Freshdesk client with typed errors and the reliability layers. |
| `internal/limiter` | Token bucket rate limiter. |
| `internal/breaker` | Circuit breaker. |
| `internal/cache` | Small cache whose entries expire. |
| `internal/coalesce` | Merges identical simultaneous calls into one. |
| `internal/mask` | Hides emails, phone numbers and (optionally) names. |
| `internal/shield` | Fences ticket text as untrusted data and flags suspicious phrases. |
| `evals` | The 10 scripted-agent behaviour checks (`make evals`). |
| `internal/brief` | Rule-based ticket summary. |
| `internal/similar` | TF-IDF similar-ticket finder. |
| `internal/audit` | One safe log line per tool call. |
| `internal/mcp` | The MCP protocol over stdin/stdout. |
| `internal/tools` | The six agent-facing tools. |
| `internal/config` | Reads settings from the environment. |
| `internal/clock` | Real and fake time, for fast tests. |
| `internal/fakefd` | Fake Freshdesk API with invented data and optional failures. |
| `cmd/freshdesk-mcp` | The connector program. |
| `cmd/fake-freshdesk` | The fake server program. |
