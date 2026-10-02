# Merchant playbook: rolling this out with a real merchant

Written as the Forward Deployed Engineer (FDE) who sits with the merchant's support team. The aim is a support agent the team trusts, reached in small safe steps. Numbers below that come from this repo are **demo data** (invented tickets in the fake server), not results from any real merchant.

## 1. Questions to ask first (week 0, one 45-minute call)
- Who answers tickets today, how many people, and what hours?
- What are the five most common ticket types? Which one wastes the most time?
- What does "good" look like: a target first reply time? A refund policy the agent must never contradict?
- Which tickets must never be touched by software (legal, fraud, large refunds)?
- Where is the data: one Freshdesk account or several? Who owns it and can create an API key?
- Who is the one named person on the merchant side who will review results every week?

## 2. Access to request (and nothing more)
- A **separate Freshdesk agent account** for the connector, with a read-only role, and an API key stored in the merchant's secret store. Never an administrator's key.
- Visibility only into the groups or products in scope for the first workflow.
- No access to payment systems, customer databases or email sending at this stage.
- Agree in writing: emails and phone numbers stay masked; the audit log (tool names and timings, no customer data) is shared with the merchant on request.

## 3. Start read-only, with one workflow
Pick one: **triage**. For each new ticket the agent reads it, calls the brief, and proposes a category, a priority and a suggested next step, for a human to accept or change. Why triage first: it is frequent, easy to judge right or wrong, and a mistake costs one click, not a wrong refund.
Before any real data: run the demo and the 10 checks (`make evals`), then replay the merchant's own anonymised past tickets. Check the security notes too: the shield should flag nothing on normal tickets and flag the attack examples.

## 4. How to measure impact (agree these before starting)
Take a baseline for two weeks before the agent runs, so the comparison is fair.
1. **First-response time**: minutes from ticket created to first human reply. Expect it to fall if triage routes urgent tickets sooner.
2. **Share of tickets triaged correctly**: the support lead reviews a sample of 30 tickets a week and marks the agent's category and priority right or wrong. Correct / reviewed.
3. **Tickets overdue at end of day**: a count from the connector's own brief (the demo data has 1 overdue open ticket out of 7 open; the real number is the merchant's).
Also track one safety number: **security notes raised and whether each was a real attack**, to tune the rules.

## 5. Weekly review with the merchant (30 minutes)
- Show the three numbers against the baseline, with the sample tickets behind them.
- Go through every wrong triage and every shield flag; decide: fix a rule, change the instructions, or leave it.
- Ask the support lead: what did the agent make easier, and what annoyed the team?
- Agree one change for the next week. Write it down. Do not change several things at once or you cannot tell what helped.

## 6. When to add write access, and keeping a human in the loop
Add a write action only after at least four weekly reviews with triage accuracy the merchant calls good enough, no unexplained surprises in the audit log, and a written yes from the merchant's owner.
- First write action: **draft a reply as a private note**, never a sent reply.
- Next: add a tag or change priority. Each action is a separate, reviewed tool, with its own rollout and its own metric.
- Always keep a **human approval step**: the agent proposes, a named person approves, and the approval is recorded. Money movement (refunds) stays human-only until the merchant decides otherwise in writing.
- Keep a one-step way back: turning the write tool off must leave the read-only connector working.

## 7. What I would watch for
Ticket text is written by outsiders. The shield fences it and flags attack phrases, but it is a lowering of risk, not a guarantee. That is why read-only comes first and why humans approve every write.
