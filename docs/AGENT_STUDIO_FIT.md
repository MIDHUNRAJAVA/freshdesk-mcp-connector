# How this fits an agent platform such as Agent Studio

I have not seen Agent Studio's internals or documentation. Everything here describes a generic MCP host. The config below is a generic example, not Agent Studio's real format.

## How it plugs in as a private connector for one merchant

- **One process per merchant.** The platform starts `freshdesk-mcp` as a child process and talks MCP over standard input and output. It opens no network port.
- **Per-merchant settings** arrive as environment variables: the merchant's Freshdesk subdomain and an API key from the platform's secret store, plus an optional request limit. The key is never written to the repo or the image.
- **Least access.** The key belongs to a separate Freshdesk agent account with a read-only role, so Freshdesk itself limits what the connector can see.
- **The platform's agent** lists the six tools (spec in [mcp-tools.json](mcp-tools.json)) and may call them. It cannot write: the client has no write code.

```json
{
  "mcpServers": {
    "freshdesk-<merchant>": {
      "command": "/path/to/bin/freshdesk-mcp",
      "env": {
        "FRESHDESK_DOMAIN": "<merchant subdomain>",
        "FRESHDESK_API_KEY": "<from the secret store>",
        "FRESHDESK_REQUESTS_PER_MINUTE": "40"
      }
    }
  }
}
```

## A week-one rollout

This is a plan, not something I have done with a merchant. It follows [MERCHANT_PLAYBOOK.md](MERCHANT_PLAYBOOK.md).

- **Day 1:** a call with the merchant (questions below). Create the read-only Freshdesk agent account and key.
- **Day 2:** run the connector against the merchant's account for the first time. Check list, get, search and the rate-limit headers against what Freshdesk really returns, and fix small differences. This is the step I have not been able to do yet.
- **Day 3:** replay a sample of the merchant's past tickets (anonymised if they prefer). Check masking and that the shield flags nothing on normal tickets.
- **Day 4:** switch on one read-only workflow, ticket triage, with a human accepting or changing every suggestion.
- **Day 5:** first review with the merchant: first-response time baseline, a sample of suggestions marked right or wrong, and the audit log.

## What I would verify with the merchant first

- Which Freshdesk plan they are on, and its API rate limit (the connector's default of 40 a minute is a cautious guess, not a Freshdesk number).
- Who can create the key, and which groups or products the first workflow may see.
- Which tickets must never be touched by software (legal, fraud, large refunds).
- Whether their tickets contain personal data beyond emails and phone numbers (addresses, card or account details), since only emails and phone numbers are masked.
- Who on their side reviews results every week.

## Deliberately not built yet

| Not built | Reason |
|---|---|
| Write access (replies, tags, status changes) | Trust has to be earned first. The playbook adds it only after several good weekly reviews, starting with a private draft note and a human approval step. Refunds stay human-only. |
| OAuth | As I understand Freshdesk's API v2, it uses an API key, so OAuth would be unused code. If a merchant's tool needs it, a token source can replace the key header in one place. |
| Webhooks and live updates | The agent reads when asked, which is simpler to secure and test. Webhooks need a public receiver, which widens the attack surface; worth adding once there is a workflow that needs "what changed". |
| Many merchants in one process | One process per merchant keeps keys, caches and rate limits separate. A shared service would need a secrets lookup per merchant. |
| Real-account testing | Needs a Freshdesk account; see [REAL_TESTING.md](REAL_TESTING.md). |
