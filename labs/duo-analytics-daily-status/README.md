# Duo Analytics Daily Status: report renderer

A replacement for the Duo Analytics daily status email. It takes the
collector's fleet-status model as JSON and renders two things from the same
model:

- **`--style email` (default)** — what actually gets mailed. Plain, table-based,
  inline-styled, one page, no scrolling on a normal inbox width: a status line,
  a one-sentence count, and one row per open customer action with its title
  and lead sentence. Matches the shape of a Duo admin alert email, not a
  dashboard — no chrome, no matrix, no cards, no dark mode. Lab/non-production
  items and informational notes are folded into a single footnote line, and
  a footer link points at the full report for anyone who needs the actual
  recovery commands.
- **`--style full`** — the dashboard: an action queue with every recovery
  command, a tenant-by-stream freshness matrix, and exception-only tenant
  detail. Meant to be linked from the email's "View full report" line, not
  mailed itself.

```bash
python render.py fleet-status.json -o daily-status.html            # email (default)
python render.py fleet-status.json --style full -o report.html     # full dashboard
```

Set `report.full_report_url` in the model (optional) to point the email's
link at wherever the full report is hosted; without it the email just names
the command to generate one.

## Why the old format failed

| Problem in the original email | What the renderer does instead |
| --- | --- |
| 32 printed pages for 8 tenants; ~280 check rows, 110 of them green | Exceptions only. Green rows collapse to a count and a one-line list. |
| The full recovery runbook (docker restart, compose, fallback pull) repeated in every failing row, ~40 times | Commands appear once per action, parameterised by tenant slug, plus a five-line runbook card. |
| Fleet headline truncated mid-word (`recover DLS: \`docker rest`) | Headlines are built from structured issues, never by cutting a string at N characters. |
| Values like `11,904.0rows`, `0.0rows`, `10.0frameworks`, `5,983.6h` | Units are typed: ages render as `9m`, `1.4h`, `249d`; counts as `11.9K`; sizes as `179.4 GB`. |
| Floating sections overlapping on print (Security Posture drawn over Cost & Usage) | Single-column block layout, no floats, no absolute positioning. Prints and emails cleanly. |
| `GREEN` for a container that restarted 52 seconds ago with `health: starting` | Anything not `healthy` renders WARN, and a note in the action queue says the freshness numbers predate the restart. |
| `GREEN scheduled_tasks 0 tasks` (a vacuous pass) | Zero tasks seen renders WARN with the words "blind spot". |
| Same-tenant facts duplicated: Duo releases repeated 8 times | Fleet facts appear once. |
| Thresholds shown only on failing rows | Every freshness cell shows its limit where one applies, so a 35h GREEN on a lab tenant is explainable. |
| Colour is the only status signal | Every status carries a glyph and a word (`■ ERR`, `▲ WARN`, `● OK`). |
| Light theme, mixed timestamp formats with microseconds | Dark-first palette (blue-black ground, deep green OK, yellow accent at 12.7:1). Timestamps are `YYYY-MM-DD HH:MM` UTC everywhere. Light tokens are provided for print and for an explicit `data-theme="light"` toggle. |

## Usage

```bash
pip install jinja2
python render.py fleet-status.json -o daily-status.html --anonymize   # neutral tenant labels, for sharing
```

`samples/fleet-status.sample.json` is a real run shape with tenant identity
replaced by neutral labels; `samples/fleet-status.sample.email.html` and
`samples/fleet-status.sample.full.html` are its two rendered forms.

## Model schema

Top level:

```
report    product, kind, date, generated_at (ISO, UTC), overall, run_id,
          collect_seconds, collector_errors, confidential
summary   tenants, ok, warn, err            (check counts)
fleet     duo_status {status, note}, duo_releases_7d, latest_release, deploys_24h,
          infra {host, service_name, disk, containers[], scheduler, nssm}
tenants[] see below
```

Per tenant:

```
slug, name, account, edition, status (RED|AMBER|GREEN), lab (bool)
streams.{auth|telephony|admin|trust_monitor}
    freshness   status, last_seen, age_h, threshold_h, no_data
    ingest      status, rows_24h, lifetime
    dow         status, yesterday, median
    checkpoint  status, at, age_h, missing
pull            status, completed_at, age_h, auth_rows, tel_rows
db              size_mb, wal_mb, drift {status, ok, tables, rows_stuck, fix}
quality         status, gap_days_30d, gap_dates_shown[], shapes_24h
slo.{auth_freshness|telephony_freshness|ingest_volume|container_health}
                status, warming, pct, samples, target, window
container_errors status, lines, window
security        bypass_status, bypass_codes, bypass_threshold, snapshot_at,
                users, admins, telephony_credits, admin_actions_24h
cost            status, credits_24h, telephony_txns_24h, active_users
changes         duo_releases_7d, latest_release, deploys_24h, change_log{}
frameworks[]
```

Statuses use the collector's vocabulary (`RED`, `AMBER`, `GREEN`, `INFO`) and
are mapped to `ERR`, `WARN`, `OK`, `INFO` at render time so the subject line
and the body finally agree.

## Fleet status is customer-scoped

A lab tenant or a non-production copy of a real customer (its name or slug
containing "non-production" / "nonprod", or an explicit `nonprod: true` from
the collector) never drives the fleet-wide verdict or the "customer tenants"
red/amber/ok tile — `is_nonprod()` in `render.py` decides this, preferring an
explicit `nonprod` field, then the `lab` flag, then a name/slug match. Its
problems still appear in full: its own action-queue entries (schema drift,
never-bootstrapped, stalled streams), its own row in the stream matrix under
a separate "Lab & non-production tenants" heading, and its own tenant detail
section. It just cannot make the headline say a customer is affected when
none is. The fleet strip shows the excluded count and its err/warn split
alongside the customer tally, so nothing is hidden, only kept out of the
verdict.

## Deprecated streams

`DEPRECATED_STREAMS` in `render.py` lists streams the collector may still
emit but that must never alert. Trust Monitor is deprecated in Duo, so its
freshness, ingest, volume and checkpoint checks are dropped from the action
queue, the matrix, the issue lists and the fleet counts. The dropped checks are
reported as `retired` in the fleet strip so the totals still reconcile with the
collector's, and tenant and overall status are recomputed from what remains.
The collector should stop emitting the stream too; the renderer guard is the
backstop, not the fix.

## How the action queue is derived

`derive_actions()` in `render.py`:

- **Per tenant, one action** covering every stream whose freshness or 24h
  ingest is not green. Streams with `no_data` and a missing checkpoint are
  described as "never bootstrapped"; streams that had data are "stalled". If
  auth is still flowing the text says so, because that is the tell that the
  container is alive and only a stream is dead. Checkpoints older than 7 days
  are named with their age, because that is the tell that DLS has not been
  the real source of that stream for a long time.
- **Schema drift** is folded into the tenant's action and its fix command is
  listed first, since nothing lands until it runs.
- **API pull older than 24h** becomes one fleet action listing the tenants.
- **Containers not `healthy`** and **SLO samplers with no history** become
  INFO items: things to know, not things to run.
- Ordering: severity, then customer tenants before lab tenants, then streams
  before everything else.

## Rendering constraints

- No CSS grid or floats for document structure, no absolute positioning, so the
  page survives print-to-PDF and mail clients.
- `<details>` for tenant sections degrades to always-open where unsupported.
- Web fonts (IBM Plex Sans / Mono) load from Google Fonts with system
  fallbacks; the page is fully legible without them.
- All external tenant text is HTML-escaped by the template engine.
