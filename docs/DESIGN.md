# Why I built it this way

Each section gives a decision, the reason in plain words, and what it costs.

## Read-only, with no write code at all
The client has no function that sends anything other than a GET request. Hiding a write behind a setting would still leave the code there, and a setting can be changed by mistake. With no write code, "the agent cannot change tickets" is a fact you can check by reading one file.
*Trade-off:* the agent cannot draft or send replies. That is a deliberate first step; a write tool could be added later as a separate, reviewed tool with its own approval step.

## Masking personal details before the agent sees them
Emails and phone numbers are replaced in ticket text, replies and requester fields, inside the connector, so the raw values never reach the agent or its logs. Masking happens before text is cut to length, so a cut cannot split an address and let part of it slip through. The similar-ticket tool also masks first, so the "shared words" it reports cannot contain parts of an email.
*Trade-off:* patterns are simple, so they can over-mask (a long order number may look like a phone number) and can miss unusual formats or other personal details such as street addresses. I chose over-masking as the safer mistake. Names are only masked if you ask.

## Retries with growing, randomised waits, and respect for Retry-After
When Freshdesk says "too many requests" or has a temporary error, trying again soon usually works. Each wait doubles, up to a limit, and is randomised so many clients do not all retry at the same instant. If Freshdesk sends a `Retry-After` header, that instruction is used instead of my guess. If it asks for a very long wait, the connector returns a clear "rate limited" error instead of freezing the agent. Client errors such as 401 and 404 are never retried because trying again cannot fix them.
*Trade-off:* a retried call is slower. The wait is bounded by the retry count and the time limit.

## A rate limiter in front of everything
A token bucket allows a short burst and then a steady number of requests per minute. It is better to stay under the limit than to hit it and be told off. The right number depends on the Freshdesk plan, so it is a setting, not a built-in claim.
*Trade-off:* if the setting is too low, calls are slower than they need to be.

## A circuit breaker
If Freshdesk is failing, more requests only make it worse and make the agent wait for nothing. After a few failures in a row the breaker "opens" and refuses calls immediately for a cooldown. Then it lets one test call through; if that works, normal service resumes. Rate-limit answers (429) do not count as failures because the server is alive. Errors like 404 do not count either.
*Trade-off:* for a short time, calls fail fast even if Freshdesk has just recovered.

## A short cache
Agents often ask for the same thing twice in a few seconds. Answers are kept for 30 seconds by default (raw response bytes, so a caller changing its copy cannot affect later readers). Errors are never cached.
*Trade-off:* data can be up to 30 seconds old. Set the time to `0s` to turn it off.

## Merging identical simultaneous requests
If several calls for the same thing arrive together, one request is sent and everyone shares the answer. This is what Go's "singleflight" does; I wrote a small version so the idea is visible and tested.
*Trade-off:* if the call that is doing the work is cancelled, those waiting get its error and can simply ask again. A waiting caller can give up on its own without stopping the shared request.

## Hard caps on how much comes back
Every tool result is limited in size (default 40,000 bytes). The tool keeps as many items as fit and says that it cut the rest. Long ticket texts are cut. The server adds a last safety check on top, so even a mistake in one tool cannot send a huge result. Search is also bounded by Freshdesk itself: 30 per page, 10 pages.
*Trade-off:* the agent may need to page or narrow its query.

## An audit log with no customer data
Each call writes one line: tool, argument names (not values), time, duration, result size, outcome. It answers "what did the agent do?" without becoming a second copy of the data.
*Trade-off:* you cannot reconstruct exactly what was asked from the log alone.

## "Smart" tools that are plain rules and arithmetic
The brief and the similar-ticket finder run locally and give the same answer for the same input. That makes them easy to test, free to run, and there is no customer text sent to another service. The brief's next-step suggestion comes from a short list of rules (who is waiting, overdue or not, how long since the last reply) that you can read in one file. The similarity finder uses TF-IDF: words that are rare across tickets count for more, and it can name the shared words that caused a match.
*Trade-off:* neither understands meaning. "Refund" and "money back" are different words to TF-IDF. The suggestions are hints, not decisions.

## API key, not OAuth
Freshdesk's API uses a key as the basic-auth username. So that is what is implemented. The key comes only from the environment, and the config object prints `[redacted]` so a stray log line cannot leak it. The connector also refuses to send the key over plain http unless the address is localhost.

## Standard library only, small packages
No outside dependencies means it builds offline and there is nothing to audit but this code. Each package does one job (see the table in the README) so each can be read and tested alone.
