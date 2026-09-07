# Lessons learned

Append-only. One entry per incident, decommission, or near-miss. The point of
this file is that the second time something happens, it's faster than the
first — so write down what slowed you down, not just what fixed it.

Format per entry: what happened, what the process didn't cover, what changed
because of it, what's still open.

---

## 2026-09-07 — Centene decommission: no runbook existed

**What happened.** Centene left the book of business. The instruction was
"stop Centene duologsync" — straightforward on its face. It wasn't, because
nothing had ever been decommissioned before and none of the following existed:

- A checklist for what "decommission a tenant" actually covers beyond
  stopping a container.
- Any way for the daily status or the observability report to stop treating
  a departed customer as a broken one. Every check on Centene's pipeline
  would have kept firing RED indefinitely — the pipeline was *supposed* to
  be stopped, but the report had no concept of "stopped on purpose."
- A distinction between stopping the local puller and closing the account.
  The Duo Admin API integration key isn't touched by anything on LEGION_01;
  as long as it's live, the credential still works.
- A default answer for what happens to the tenant's stored data. This one
  surfaced a genuinely non-technical question: the local ~179 GB is a
  read-only mirror of Centene's Duo-hosted logs, and the working
  interpretation this round was that Duo's own retention policy on the
  source data is the relevant clock, not a separate destruction obligation
  on the local copy. That interpretation was made in the moment, from
  memory of Duo's retention window, not from re-reading the actual contract
  or BAA — it should be confirmed against the real document, not treated as
  settled because it sounded reasonable.

**What changed because of it.**

- `runbooks/decommission-tenant.md` — the checklist that should have existed
  before this one. Five steps: stop the pipeline, stop the alerting, close
  the account, decide the data question deliberately (with a checklist of
  what to check, not what to conclude), verify.
- `render.py` gained an `offboarded` tenant status, distinct from the
  existing `nonprod` exclusion. A non-prod tenant's problems still generate
  actions because they still need fixing; an offboarded tenant's don't,
  because nothing is going to fix them. Excluded from the fleet verdict, the
  customer tally, the matrix, and the action queue — but never silently:
  both report styles list offboarded tenants by name, date, and reason, so
  the drop reads as a decision on the record, not data quietly vanishing.
- This file, so the next one of these isn't a cold start either.

**Still open.**

- The Duo Admin Panel API-key deactivation for Centene is a manual step
  outside anything this session can verify — confirm it was actually done,
  not just documented as a step.
- The retention interpretation above needs a second look against the actual
  contract/BAA language, by someone with the document in hand, not from
  memory of Duo's general retention policy.
- The broader pattern this incident points at — onboarding and offboarding
  are both entirely manual today, credentials live wherever they were typed
  rather than in a vault, and there's no automated bridge between "a
  contract changes" and "the pipeline reflects it" — is tracked as its own
  design problem, not a fix to bolt on here. See the architecture roadmap
  for automated onboarding, credential vaulting, and the SSF bridge.
