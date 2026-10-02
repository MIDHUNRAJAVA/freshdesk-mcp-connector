# Limitations (honest list)

**Not tested against a real Freshdesk account.** The fake server follows Freshdesk's public API documentation as I understood it. Real responses may differ in small ways (field names, which fields are present by default, embedded conversation counts). Expect to fix small things on the first real run.

**Authentication**
- API key only. I believe Freshdesk's API v2 is key based, so there is no OAuth flow here. The key's Freshdesk role decides what the agent can see; use a least-privilege agent account.
- One key, one account per running connector. No per-merchant credential storage.

**Reading data**
- Search returns 30 results per page and Freshdesk allows 10 pages, so at most 300 matches are reachable for one query. Narrow the query to see others.
- The list endpoint, by default, returns only recent tickets (Freshdesk documents a 30-day default). Use `updated_since` for older ones.
- The list endpoint cannot filter by status. The `status` option filters the returned page only; use search to filter across all tickets.
- Freshdesk limits how many replies it embeds in a single ticket fetch. Use `freshdesk_list_conversations` for long threads. The brief reads up to 300 messages and says when the thread is incomplete.
- Only a subset of fields is returned. No attachments, no custom fields, no companies or contacts beyond the requester, no knowledge-base articles, no time entries.
- HTML bodies are not used; only the plain-text versions.

**Safety**
- Masking covers emails and phone numbers. It does not detect street addresses, card numbers written as words, government IDs, or names (names are optional). Free-text ticket bodies can contain anything.
- Patterns can over-mask: a long digit string such as an order number may be hidden as if it were a phone number.
- Masking does not stop an agent from acting on what it reads. Treat ticket text as untrusted input: a customer can write instructions inside a ticket. The connector has no write tools, which limits the damage, but the agent host should still treat tool output as data.

**Reliability**
- Rate limits depend on the Freshdesk plan and can differ per endpoint. I did not hard-code any numbers; `FRESHDESK_REQUESTS_PER_MINUTE` is yours to set.
- The cache and the request merging live in memory in one process. Nothing is shared between processes and everything is lost on restart.
- The circuit breaker is global to the connector, not per endpoint.
- Cached data can be up to the cache time old (30 seconds by default).

**The smart tools**
- The brief and similar-ticket finder are rule-based and word-based. They do not understand meaning, other languages are handled only as far as words split on letters, and the suggestions are hints.
- Similar tickets are searched only among the most recently updated tickets Freshdesk returns (up to 300), not the whole history.
- Reading the whole conversation for a brief costs extra requests (one per 100 messages).

**Not built**
- No webhooks, no incremental sync, no write actions, no attachment download, no multi-account support, no metrics endpoint.
