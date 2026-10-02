# Draft answers for the Razorpay form (Forward Deployed Engineer, Agent Studio)

Review every line. Sentences marked [CONFIRM] contain something only you can verify. Replace the link placeholder after you push the repo.

## Quick facts

- **Experience bracket:** 0-2 years
- **Strongest programming language:** Go (also Rust and Python)
- **Willing to work on-site in Bangalore, and to relocate:** Yes
- **Assignment 3 link (GitHub repo):** [PASTE YOUR GITHUB REPO LINK HERE]

## Describe an LLM, agent or workflow-automation solution you have built

At my current company I have built MCP enablement workflows: the pieces that let AI agents be onboarded and connect to tools through MCP, with multi-tenant access and authentication handled so that each tenant only reaches what it should. [CONFIRM: check that this matches what you actually own and describe it in your own words. I left out the company name, numbers and any detail I do not know.]

For this assignment I built a private, read-only MCP connector for Freshdesk in Go, using only the standard library. An agent can list, get and search tickets and read their conversations, and two extra tools give a short rule-based brief of a ticket and a list of similar tickets found with a hand-written TF-IDF comparison. Neither extra tool calls an outside AI service.

The part I focused on is the guardrails, because an agent should not be trusted with a help desk by default:
- **Read-only:** the client has no write methods at all, so the agent cannot change or reply to a ticket.
- **Masking:** emails and phone numbers are hidden before data reaches the agent; it is on by default.
- **Rate limits and reliability:** a client-side rate limiter, retries that honour Retry-After with backoff and jitter, a circuit breaker, a short cache and merging of identical simultaneous calls.
- **Typed errors:** auth failed, not found, rate limited, upstream error and circuit open are separate error kinds, and the agent gets a plain sentence for each, never the API key.
- **Caps and audit:** every tool result has a hard size cap, and every call writes an audit line with no customer data in it.

I evaluated it with automated tests and a fake Freshdesk server of my own. The tests cover the auth header, pagination, search limits, 429 with Retry-After, server-error backoff, giving up after the maximum retries, 401 and 404 mapping, masking (including a check that raw emails and phone numbers never appear in any tool output), and a full MCP session over in-memory pipes; they pass with the race detector on. The fake server can fail on purpose (rate limits, errors, an outage) so I could watch the retries and the circuit breaker work. I have not run it against a real Freshdesk account, so the first real run may need small fixes. [CONFIRM: you are comfortable stating this limitation.]

## Are you comfortable leading technical conversations with a customer and building the solution yourself?

I build solutions end to end myself, from design to code to tests, and I lead technical discussions with internal teams, for example on how access and authentication should work for agents. I have not yet owned a relationship with an external, merchant-facing customer. I want to grow into that, and I see this role as the way to do it: I would pair with a senior colleague at first, and I am comfortable learning the customer's problem well before proposing a design. [CONFIRM: the pairing sentence is a suggestion, remove it if it does not reflect how you would like to put it.]

## Notes for you

- The repo is at /home/welcome/personal/freshdesk-mcp-connector. Nothing has been pushed anywhere.
- Before submitting, check that the README statement "not run against a real Freshdesk account" is still true. If you test it with a Freshdesk trial, update the README and LIMITS.md.
