# ADR-005: Documentation knowledge service (Duo / Cisco / Identity Intelligence)

Status: proposed. Grayskull track only — see [ADR-001](adr-001-local-first-apple-intelligence-dual-track.md)'s
dual-track table. There is no Cisco-side deliverable here: this is a personal
research tool, single user, self-hosted on fortress. Nothing in it may depend
on a Cisco SKU or employee entitlement, per ADR-001 §"Dual-track ownership"
rule 1.

## Decisions

1. **Index and cite, never mirror.** Store chunked excerpts with embeddings and
   a citation link back to the source page — enough text to make a chunk
   retrievable and quotable, not a verbatim copy of the whole page. This is
   the lower-risk choice on two axes at once: it doesn't redistribute Cisco's
   copyrighted documentation, and it means every answer this feeds an
   assistant carries a link the user can verify against, which matters more
   for onboarding than a slightly bigger excerpt would.
2. **Single-user, bearer-token auth on the MCP server.** No Duo Agentic
   Identity, no OAuth-based multi-tenant gateway, no NHI model — that
   machinery exists in the family CastleOps design because it has multiple
   human principals and an orchestrator dispatching to domain agents. This
   service has one principal and no delegated agents. A generated bearer
   token the user holds is the whole auth story until a specific client
   (see §6) is proven to require more.
3. **Fetch and diff are deterministic jobs, not agents with judgment.**
   Detecting that a page changed is a content-hash comparison, not a
   decision an LLM needs to make. Consistent with ADR-001's principle that
   the fewer places in the pipeline an LLM has agency, the smaller the
   attack surface — a local embedding model is used for retrieval, not for
   deciding what to fetch or when.
4. **Prefer the source's own API over scraping, wherever one exists.**
   Two of five sources already have one (below). Scraping is the fallback,
   not the default.
5. **Runs on fortress, reachable only through Cloudflare Tunnel + Access.**
   No inbound port on the Mac mini. Cloudflare Access gates who can even
   reach the tunnel (the user's own identity); the MCP server's bearer
   token is the second, independent check behind it.

## Relationship to the v1 interface contract

`castleops/contract/v1` landed on this branch after this ADR was first
drafted. It doesn't fit, and shouldn't be forced to: `tool.schema.json`
requires `domain` from a closed enum (`network`, `calendar`, `iot`, `admin`)
tied to a `castleops-<domain>` worker NHI, and `kind` distinguishes
`read`/`write`/`elevate` because writes need a presence-key assertion and
elevation enters break-glass. That machinery exists because the family
system has multiple human principals and an orchestrator dispatching to
workers it doesn't fully trust. This service has one principal and no
orchestrator — there is nothing for a presence key or a Cedar policy to
adjudicate between.

So this stays outside the v1 contract for now, as its own standalone MCP
server with its own bearer token, not a `castleops-docs` worker behind the
gateway. Revisit that if the shape of the need changes — specifically, if a
family-system agent (say, `castleops-admin` looking up a manual) ever needs
to call into this service as a tool. At that point it earns a `domain:
"admin"`, `kind: "read"` entry in the registry like any other read tool, and
the gateway's existing machinery is the right place for that request to
flow through. Building that path speculatively now, before anything needs
it, is exactly the kind of complexity ADR-001 argues against.

## Sources (verified, not guessed)

| Source | Content | Location | Update signal |
|---|---|---|---|
| Duo docs | Admin/product/API documentation | [duo.com/docs](https://duo.com/docs), [duo.com/docs-and-resources](https://duo.com/docs-and-resources) | Sitemap diff |
| Duo Admin API / Auth API reference | API reference | [duo.com/docs/adminapi](https://duo.com/docs/adminapi), [duo.com/docs/authapi](https://duo.com/docs/authapi) | Page diff |
| Duo Knowledge Base | Thousands of support KB articles | [help.duo.com](https://help.duo.com) | Sitemap/search-index diff — confirm crawl politeness before scheduling |
| Duo blog | Product & security blog | [duo.com/blog](https://duo.com/blog) | RSS if published, else sitemap diff |
| Duo software release notes | Per-product release notes | [duo.com/docs/releasenotes](https://duo.com/docs/releasenotes) (index; Duo Desktop, DNG, Auth Proxy, RDP each have their own page) | Page diff |
| Duo Community release notes | Community-posted release notes | [community.duo.com/c/release-notes/27](https://community.duo.com/c/release-notes/27) | Discourse RSS (standard on this platform) |
| Duo Product Security Advisories | Duo's own PSA list | [duo.com/learn/psa](https://duo.com/learn/psa) | Page diff |
| Cisco PSIRT advisories (Duo) | Vulnerability advisories | [cisco.com/.../security/duo/products-security-advisories-list.html](https://www.cisco.com/c/en/us/support/security/duo/products-security-advisories-list.html) | **Cisco's openVuln API** — a real, documented, key-authenticated REST API for PSIRT advisories. Use it instead of scraping HTML; register for an API key via the Cisco API Console first. |
| Duo status | Incidents, component status | [status.duo.com/api/v2/status.json](https://status.duo.com/api/v2/status.json) (live JSON), [status.duo.com/history.rss](https://status.duo.com/history.rss) (incident history) | Poll the JSON API + the RSS feed — both are real, documented endpoints, no scraping needed |
| Cisco Identity Intelligence (Oort) KB | CII docs, blogs, public API reference | [docs.oort.io](https://docs.oort.io) (+ `/blogs`, `/public-api`, `/integrations`) | Sitemap diff |
| Astrix Security — Learn | NHI (non-human identity) security: guides, glossary, blog — API key / OAuth app / secrets-sprawl risk research | [astrix.security/learn](https://astrix.security/learn/) | Sitemap/RSS diff — confirm `robots.txt` and crawl politeness before scheduling. **Not verified from this session**: astrix.security is blocked by this sandbox's egress proxy, so the actual page structure, sitemap, and feed availability need confirming from fortress directly before this row is built, not assumed from the row above. |
| WideField Security | Identity threat detection/response docs — identity lifecycle (at rest / in motion / in use) across human, non-human, and AI-agent identities | [widefield.ai](https://www.widefield.ai/) | Page diff. **Time-sensitive**: standalone sale/licensing ended July 31, 2026 per their own site — content is likely being folded into Cisco product pages or pulled down on a schedule this ADR doesn't control. Fetch and archive what's there before it moves, don't wait for a "stable" crawl target that may not exist. |
| Galileo (AI observability/eval) | Docs, API reference, integration guides for the agent-observability platform Astrix/WideField telemetry is explicitly headed toward | [docs.galileo.ai](https://docs.galileo.ai/what-is-galileo) | Sitemap/page diff |
| Splunk Enterprise & Cloud Platform docs | Admin/search/product documentation — an operational tool in daily use, not just identity-adjacent research | [help.splunk.com](https://help.splunk.com/en), [docs.splunk.com](https://docs.splunk.com/Documentation) (versioned/legacy) | Page diff |
| Splunk release notes / what's new | Per-version release notes, Splunk Cloud Platform service updates | [docs.splunk.com](https://docs.splunk.com/Documentation) release-notes pages per product/version | Page diff |
| Cisco ISE (Identity Services Engine) docs | Admin/config docs for the network-access-control side of Cisco's identity stack — 802.1X, TrustSec, posture, pxGrid | [cisco.com/.../identity-services-engine/series.html](https://www.cisco.com/c/en/us/support/security/identity-services-engine/series.html) | Page diff |
| Cisco ISE release notes | Per-release notes (3.1–3.5 current) | [cisco.com/.../products-release-notes-list.html](https://www.cisco.com/c/en/us/support/security/identity-services-engine/products-release-notes-list.html) | Page diff |
| Cisco Secure Access docs | Unified SSE platform docs — ZTNA/Private Access, Secure Web Gateway, CASB, FWaaS, DNS-layer security, DLP (web/cloud/email/endpoint), all under one product now | [securitydocs.cisco.com/docs/csa](https://securitydocs.cisco.com/docs/csa/) + [cisco.com/.../secure-access/series.html](https://www.cisco.com/c/en/us/support/security/secure-access/series.html) | Sitemap/page diff |
| Cisco Umbrella docs | Legacy DNS-security/SIG brand — still the doc tree of record for anything not yet migrated, includes Umbrella's own DLP pages | [cisco.com/.../umbrella/series.html](https://www.cisco.com/c/en/us/support/security/umbrella/series.html) | Page diff |
| Cisco Secure Access for agentic AI (MCP Zero Trust) | Registering AI-agent identity in Duo IAM, MCP gateway policy enforcement, tool-level least-privilege, short-lived JIT tokens for agent-to-MCP-server auth | [cisco.com/.../securing-agentic-ai](https://www.cisco.com/site/us/en/solutions/artificial-intelligence/security/securing-agentic-ai/index.html), [blogs.cisco.com](https://blogs.cisco.com/?p=492855), [newsroom.cisco.com](https://newsroom.cisco.com/c/r/newsroom/en/us/a/y2026/m02/cisco-redefines-security-for-the-agentic-era.html) | **No stable product doc tree yet** — this is solution briefs, blog posts, and newsroom announcements (Feb–Mar 2026), not versioned documentation like the rows above. Re-check for a real docs.cisco.com/securitydocs section once the feature ships past announcement stage; until then, page diff on these specific URLs, expect it to move. |

Private Access, SWG, DNS security, and DLP don't get their own rows beyond
the two above: they're capabilities inside Secure Access (and, for now,
still partly inside Umbrella), not separate products with separate doc
trees — DLP specifically lives at `securitydocs.cisco.com/docs/csa/` right
alongside everything else in that row. Cisco's own migration guide
(Umbrella → Secure Access, via Security Cloud Control) is what actually
explains which capability lives where at any given moment — index it, don't
hand-maintain that mapping here.

**Worth noticing, not acting on**: Cisco's MCP Zero Trust design — register
agent identity, route all tool traffic through a gateway, issue short-lived
JIT tokens, enforce tool-level (not just server-level) authorization — is
the same shape of problem CastleOps's own gateway already solves on the
Grayskull track (Cedar PDP, DPoP, MCP Streamable HTTP, per-tool policy in
`contract/v1`). Reading Cisco's approach here is legitimate prior art for
comparison. It is not, per ADR-001's dual-track rule, a reason to add a
Cisco dependency to CastleOps.

## Cisco acquisitions in scope

The last four rows above are here because Cisco bought the companies (or, for
Splunk, because it's a tool actually run day to day), on the same "acquired
company, keep indexing its public docs at the acquired domain" precedent
already set by Oort → Cisco Identity Intelligence — not because each was
independently chosen as background reading:

| Company | Announced / closed | What Cisco said it's for |
|---|---|---|
| [Oort](https://blogs.cisco.com) | 2023 | Became Cisco Identity Intelligence — identity risk correlation across IdPs |
| Splunk | 2023 | SIEM/observability platform; destination for the identity/session telemetry the three rows below all explicitly feed |
| [Astrix Security](https://blogs.cisco.com/news/cisco-announces-intent-to-acquire-astrix-security) | Announced May 4, 2026; closed June 29, 2026 (~$350–400M) | NHI discovery/lifecycle/threat detection, integrating into **Cisco Identity Intelligence, Duo, and Secure Access**, with agentic telemetry feeding Splunk |
| [WideField Security](https://blogs.cisco.com/news/cisco-announces-intent-to-acquire-widefield-security) | Announced June 18, 2026 | Identity/session/activity telemetry normalization and correlation, integrating into **Splunk's Agentic SOC** |
| [Galileo Technologies](https://blogs.cisco.com/news/cisco-announces-the-intent-to-acquire-galileo) | Announced April 9, 2026; closed May 22, 2026 | Agent-development-lifecycle observability/eval/guardrails, folding into **Splunk's** observability portfolio |

Four identity- or telemetry-adjacent acquisitions in three years (Oort,
Galileo, Astrix, WideField) — all converging on Splunk as the place the data
lands — is a standing signal this list needs revisiting periodically, not a
one-time reconciliation. See Build order, step 0, below.

Correction from this ADR's prior revision: Galileo was ruled out here as
"AI-observability, not identity." That was wrong — Astrix and WideField's own
announcements name Splunk (which Galileo now strengthens) as where their
telemetry lands, so Galileo is the same acquisition chain, not a separate
one. Observability counts. EzDubs (Nov 2025, speech translation) is still
ruled out — nothing in its own announcement connects it to identity,
telemetry, or Splunk.

**Cisco ISE is not in this table on purpose** — it doesn't belong in the
"recent acquisition chain" story. ISE traces to Cisco's 2004 acquisition of
Perfigo (Clean Access merged with RADIUS/TACACS into what became ISE 1.0 in
2011); that's 20 years and one full product generation before the
Oort→Astrix→WideField→Galileo chain above, with no telemetry relationship to
Splunk in how Cisco describes either product. It's in the Sources table
anyway, for a different and simpler reason: ISE is the network-access-control
half of Cisco's identity stack (802.1X, posture, TrustSec) that pairs with
Duo's MFA/device-trust half in real deployments — same rationale as Duo
itself being a source, not the acquisitions-in-scope rationale.

Two of these nineteen rows are already a real API (Duo status, Cisco
openVuln), not a scrape target — start there; it's the fastest path to
something working and the least likely to get rate-limited or blocked.

## Architecture

```
[Fetcher/differ — cron, per-source cadence]
    for each source: list pages (sitemap / RSS / API) -> hash each page's content
    -> compare to last-seen hash in SQLite -> unchanged: skip. changed: hand to indexer.

[Indexer]
    extract clean text -> chunk (~500-800 tokens, overlap) -> embed locally
    (Ollama embedding model already running on fortress per ADR-001 — no
    cloud embedding API) -> store {text, embedding, source_url, title,
    section, fetched_at, content_hash} in a local vector store (sqlite-vec
    or Chroma; both self-hostable, no cloud dependency)

[MCP server]
    search_docs(query, source_filter?)   -> ranked chunks, each with its citation link
    get_source_freshness()               -> last successful fetch per source
    list_recent_changes(since?)          -> pages that changed recently

    auth: single-user bearer token, checked on every call

[Cloudflare Tunnel, outbound from fortress — no inbound port]
[Cloudflare Access — gates who reaches the tunnel at all]
    -> MCP endpoint, added as a remote MCP connector in ChatGPT / Claude / Gemini / Grok
```

## Devil's advocate

- **Fortress is a single point of failure for a tool meant to help onboarding.**
  If the Mac mini is asleep or offline, the knowledge service is down. Add a
  freshness/uptime self-check using the same pattern already built for
  LEGION_01 in the Duo Analytics lab — reuse the design, don't invent a
  second one.
- **help.duo.com and some Cisco pages may rate-limit or block automated
  fetching.** Respect `robots.txt`, use a real crawl delay, identify the
  fetcher with a descriptive User-Agent, and lean on the two real APIs
  (status.duo.com, Cisco openVuln) wherever they cover the need instead of
  scraping.
- **A local embedding model on a Mac mini may retrieve worse than a hosted
  embedding API.** Accepted for v1 — "self-hosted, no cloud dependency" was
  the point of running this on fortress at all. Revisit only if retrieval
  quality is actually poor in practice, not preemptively.
- **"Index and cite" still stores verbatim excerpts.** Keep chunks to what's
  needed for retrieval and a short quotable snippet — a few sentences, not
  whole sections — as a quoting discipline, not just a storage-format
  decision.
- **Bearer-token auth may not satisfy every client's remote-MCP connector UI.**
  Some implementations increasingly expect OAuth discovery metadata even for
  a single user. Don't build that speculatively — try the bearer token
  against all four target clients first, and add a minimal self-issued
  OAuth 2.1 shim only for whichever one actually requires it.
- **The Astrix Security row was added sight-unseen.** This session's egress
  proxy blocks astrix.security outright, so its sitemap/RSS structure was
  never actually confirmed — the row is a placeholder for "fetch this
  content" until someone (or fortress, which isn't behind this sandbox's
  proxy) actually loads the page and checks.
- **Splunk's own docs are enormous** — Enterprise, Cloud Platform, and every
  admin/search-language/app-dev corner of both, spanning many major
  versions. Indexing all of it defeats "index and cite" as a curation
  discipline. Scope the crawl to what's actually used day to day (the admin
  and search-language sections relevant to this deployment, release notes,
  Cloud Platform service pages) rather than mirroring the whole doc tree —
  revisit the scope if a specific gap shows up in practice, not
  preemptively.
- **Umbrella and Secure Access will say contradictory things about the same
  feature while the migration is in progress.** A chunk from Umbrella's docs
  and a chunk from Secure Access's docs can both rank for the same query and
  disagree — one describing the pre-migration behavior, one post. Store each
  chunk's source name plainly in the citation (not just the URL) so an
  answer built from both is visibly mixed-vintage rather than silently
  wrong; revisit whether Umbrella needs pulling from the index entirely once
  Cisco's own migration guide says the cutover is complete.

## Build order

0. Before each future revision of this ADR, re-check
   [Cisco's acquisitions list](https://www.cisco.com/site/us/en/about/corporate-development/acquisitions/acquisitions-list-years/index.html)
   for anything identity/NHI/Zero-Trust-adjacent since the last pass, per
   "Cisco acquisitions in scope" above. Three hits in three years (Oort,
   Astrix, WideField) means this is a recurring maintenance step, not a
   one-off — add the source row or explicitly rule it out (as done above
   for Galileo/EzDubs), don't leave it unexamined.
1. Register for a Cisco openVuln API key; confirm `robots.txt` and crawl
   politeness for `help.duo.com` and any other page-scraped source.
2. SQLite schema: `sources`, `pages(url, content_hash, fetched_at)`,
   `chunks(text, embedding, page_id, section)`.
3. Fetcher/differ cron — start daily for docs/KB/blog, hourly for
   status.duo.com and Cisco advisories (the sources where being late
   actually costs something).
4. Local embedding + chunk indexer.
5. MCP server: the three tools, bearer-token auth.
6. Cloudflare Tunnel + Access in front; test reachability from off the LAN.
7. Add as a remote MCP connector in each of ChatGPT, Claude, Gemini, and
   Grok's web app settings; note whichever one needs more than a bearer
   token.
8. Freshness self-check, styled on the LEGION_01 observability report
   already built in `labs/duo-analytics-daily-status/`.
