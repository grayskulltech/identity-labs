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
| Astrix Security — Learn | Non-human identity (NHI) security: guides, glossary, blog — API key / OAuth app / secrets-sprawl risk research | [astrix.security/learn](https://astrix.security/learn/) | Sitemap/RSS diff — confirm `robots.txt` and crawl politeness before scheduling. **Not verified from this session**: astrix.security is blocked by this sandbox's egress proxy, so the actual page structure, sitemap, and feed availability need confirming from fortress directly before this row is built, not assumed from the row above. |

In scope alongside Duo/Cisco/CII: NHI security is the same risk category CII
correlates (identity risk across IdPs) and the same one the Pipeline
Automation & Vaulting roadmap's credential-vaulting work touches — Astrix's
material is background reading for both, not a separate product to track.

Two of these eleven rows are already a real API, not a scrape target — start
there; it's the fastest path to something working and the least likely to
get rate-limited or blocked.

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

## Build order

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
