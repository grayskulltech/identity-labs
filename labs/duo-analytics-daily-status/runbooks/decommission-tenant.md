# Decommissioning a tenant

Run this whenever a customer leaves the book of business. It has two halves:
stop the pipeline (today, low-risk, reversible) and close the account (today
or soon, mostly irreversible) — don't let the second half block the first.

Copy this file's checklist into an issue or a message thread per decommission
so there's a record of what was actually done and when. When you're done,
append an entry to `lessons-learned.md` — what this runbook didn't cover, or
got wrong, so the next decommission is shorter than this one.

## 1. Stop the pipeline (do this first, same day)

```bash
# stop the container and make sure nothing brings it back
docker stop <slug>-duologsync
docker update --restart=no <slug>-duologsync
docker rm <slug>-duologsync

# remove it from the compose file so a fleet-wide `up -d` doesn't recreate it
#   edit docker/duologsync/compose.customer-tenants.yml, delete the <slug>-dls service block

# disable the fallback API pull's scheduled task
schtasks /Change /TN "<Tenant>-Duo-Analytics-Pull" /DISABLE
# once you're sure it's permanent:
schtasks /Delete /TN "<Tenant>-Duo-Analytics-Pull" /F

# if nssm manages a tenant-specific service entry
nssm stop <Tenant>-Duo-Analytics
nssm remove <Tenant>-Duo-Analytics confirm
```

- [ ] Container stopped and removed
- [ ] Compose entry removed
- [ ] Scheduled task disabled (then deleted once confirmed permanent)
- [ ] nssm service entry removed, if one existed

## 2. Stop the alerting (same day, so no one gets paged for a customer that's gone)

Set on the tenant in the fleet-status model:

```json
{ "offboarded": true, "offboarded_at": "<ISO timestamp>", "offboarded_reason": "contract ended" }
```

`is_offboarded()` in `render.py` then drops the tenant from the fleet verdict,
the customer tally, the stream matrix, and the action queue — while still
listing it once, with the reason and date, in the report's audit section so
the exclusion is visible, not silent.

- [ ] `offboarded: true` set in the model (or in whatever the collector reads to build it)
- [ ] Confirmed the next report run shows the tenant only in the offboarded table, nowhere else

## 3. Close the account (do this soon — it's the step that actually ends access)

Stopping the local puller does **not** revoke the credential. As long as the
Duo Admin API integration key is live, anyone who has it can still pull the
customer's data.

- [ ] Deactivate or delete the tenant's Admin API integration key in the Duo Admin Panel
- [ ] Revoke any nssm/service-account credentials scoped to this tenant only
- [ ] Confirm no shared credential covered more than this one tenant (if it did, rotate it, don't just delete the tenant's reference to it)

## 4. Decide what happens to the data (this is not yours to decide alone)

This tenant's local database is a copy — the contract or BAA governs what you're
required to do with it, not convenience. Before touching it:

- [ ] Find and read the relevant retention/data-return/destruction clause (BAA, MSA, or contract)
- [ ] Note whether the data is a read-only mirror of a system with its own retention
      policy (e.g. Duo's own log retention) — if so, the local copy's own
      retention may not carry an independent destruction obligation, but it's a
      decision to make explicitly, not an assumption to skip past
- [ ] If retention has a defined end date, schedule the destruction (a dated reminder,
      not a mental note)
- [ ] If destruction is required now, write and review the deletion runbook before
      running it — it is not reversible

## 5. Verify

- [ ] Next scheduled report run: tenant appears only in the offboarded section
- [ ] No action-queue entries reference the tenant
- [ ] `docker ps -a` on LEGION_01 shows no container for the tenant
- [ ] `schtasks /Query` shows no active task for the tenant
- [ ] Duo Admin Panel shows the integration key as deactivated
