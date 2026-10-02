# What has been tested against the real thing

Most tests use a fake Freshdesk that I wrote, so they only prove the connector matches my own idea of Freshdesk. This page lists the checks I ran against real software, and what is still untested.

## Against the real Freshdesk service

One request from the connector to `support.freshdesk.com` with a deliberately invalid key (the real service, no account used):

- The real service answered `401` with `{"code":"invalid_credentials", ...}`. The connector turned this into a plain "credentials rejected, retrying will not help" message and did not retry. The key was never printed.
- **This found a real bug.** The real service sends its rate-limit headers with a decimal point (`X-Ratelimit-Total: 5000.0`). My code expected whole numbers and silently dropped the information. It now reads both forms. There is a unit test for it (`TestParseCount`) and the fake server now sends the decimal form too, so the demo matches reality.

## Against a real MCP client

The official MCP Inspector (the reference tool for MCP servers, run in command-line mode) connected to the server over standard input and output and checked:

- The handshake works and the server reports its tools.
- All 6 tools are listed with valid input schemas; required fields are marked.
- A normal call (`freshdesk_ticket_brief`) returns the brief; ticket text is fenced as untrusted.
- A missing ticket and a missing argument both come back as clear, flagged errors, not crashes.

## Not tested yet

- **An authenticated call to a real Freshdesk account** (real tickets, real search syntax, real pagination). It needs a Freshdesk account and API key. Until that is done, treat list, get, search and conversation calls as tested against the public API documentation only.
- **Agent Studio itself.** The example config in the README is a generic one.
- Behaviour under a real, sustained rate limit. The 429 and `Retry-After` logic is tested against the fake server only.
