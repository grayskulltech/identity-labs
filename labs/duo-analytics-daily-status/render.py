#!/usr/bin/env python3
"""Render a Duo Analytics fleet-status model (JSON) into the daily status HTML.

Usage:
    python render.py status.json -o daily-status.html
    python render.py status.json --anonymize -o sample.html

The model schema is documented in README.md. The renderer is deliberately
opinionated about *presentation*: it derives an action queue from the raw
checks, collapses healthy rows, and prints every recovery command exactly once.
"""
from __future__ import annotations

import argparse
import json
import re
from datetime import datetime, timezone
from pathlib import Path

from jinja2 import Environment, FileSystemLoader, select_autoescape

HERE = Path(__file__).resolve().parent

ALL_STREAMS = [
    ("auth", "Auth", "auth_logs"),
    ("telephony", "Telephony", "telephony_logs"),
    ("admin", "Admin", "admin_logs"),
    ("trust_monitor", "Trust Monitor", "trust_monitor_events"),
]
# Streams the collector still emits but that must not alert. Trust Monitor is
# deprecated in Duo, so its freshness, ingest, volume and checkpoint checks are
# dropped from the action queue, the matrix, the issue lists and the counts.
DEPRECATED_STREAMS = {"trust_monitor"}
STREAMS = [s for s in ALL_STREAMS if s[0] not in DEPRECATED_STREAMS]

# Recovery commands, printed once per action instead of once per failing row.
RUNBOOK = {
    "dls_restart": "docker restart {slug}-duologsync",
    "dls_recreate": "docker compose -f docker/duologsync/compose.customer-tenants.yml up -d --no-deps {slug}-dls",
    "pull_stream": "set DUO_TENANT_SLUG={slug} && python pull.py --logs-only   # streams: {streams}",
    "pull_admin": "python pull_activity.py --tenant {slug}",
    "drift_fix": "python schema_drift.py --tenant {slug} --apply",
}

SEVERITY_RANK = {"RED": 0, "AMBER": 1, "INFO": 2, "GREEN": 3}
SEVERITY_LABEL = {"RED": "ERR", "AMBER": "WARN", "GREEN": "OK", "INFO": "INFO"}
SEVERITY_GLYPH = {"RED": "■", "AMBER": "▲", "GREEN": "●", "INFO": "○"}

# A tenant in this set never drives the fleet-wide verdict or the customer
# red/amber/warn/ok tile: a lab tenant, or anything that is a non-production
# copy of a real customer (e.g. "NYU Langone Health (Non-Production)"). Its
# problems still show up in the action queue and its own detail row — they
# still need fixing — they just cannot make the headline say a customer is
# affected when none is.
NONPROD_NAME_HINTS = ("non-production", "non prod", "nonprod")


def is_offboarded(t: dict) -> bool:
    """True once a tenant's contract has ended. Distinct from is_nonprod: an
    offboarded tenant is excluded from EVERYTHING — the fleet verdict, the
    customer tally, and the action queue — because alerting on a decommissioned
    customer's broken pipeline is noise, not a finding. It still gets one
    transparent line in the footer (name, when, why) so the exclusion is
    auditable rather than a silent disappearance.
    """
    return bool(t.get("offboarded"))


def is_nonprod(t: dict) -> bool:
    """True if a tenant should be excluded from the fleet/customer verdict.

    Prefers an explicit `nonprod` field from the collector; falls back to the
    `lab` flag (account prefix `LAB*`) and then to name/slug matching so a
    tenant like NYU Langone's non-production copy is caught even though it
    is not a lab account.
    """
    if t.get("nonprod") is not None:
        return bool(t["nonprod"])
    if t.get("lab"):
        return True
    haystack = f"{t.get('name', '')} {t.get('slug', '')}".lower()
    return any(hint in haystack for hint in NONPROD_NAME_HINTS)


# ----------------------------------------------------------------------------- filters
def fmt_age(hours: float | None) -> str:
    if hours is None:
        return "—"
    if hours < 0:
        return "0m"
    if hours < 1:
        return f"{round(hours * 60)}m"
    if hours < 48:
        return f"{hours:.1f}h".replace(".0h", "h")
    return f"{hours / 24:.0f}d"


def fmt_int(n) -> str:
    if n is None:
        return "—"
    return f"{int(n):,}"


def fmt_compact(n) -> str:
    if n is None:
        return "—"
    n = float(n)
    for unit, div in (("M", 1e6), ("K", 1e3)):
        if abs(n) >= div:
            v = n / div
            return f"{v:.1f}{unit}".replace(".0", "")
    return f"{int(n):,}"


def fmt_mb(mb) -> str:
    if mb is None:
        return "—"
    if mb >= 1024:
        return f"{mb / 1024:.1f} GB"
    if mb >= 1:
        return f"{mb:.0f} MB"
    return f"{mb * 1024:.0f} KB"


def fmt_ts(iso: str | None, with_date=True) -> str:
    if not iso:
        return "—"
    ts = datetime.fromisoformat(iso.replace("Z", "+00:00"))
    return ts.strftime("%Y-%m-%d %H:%M") if with_date else ts.strftime("%H:%M")


def fmt_duration(seconds: float | None) -> str:
    if seconds is None:
        return "—"
    m, s = divmod(int(round(seconds)), 60)
    return f"{m}m {s:02d}s" if m else f"{s}s"


def sev_label(s: str) -> str:
    return SEVERITY_LABEL.get(s, s)


def sev_glyph(s: str) -> str:
    return SEVERITY_GLYPH.get(s, "")


def humanize_check(name: str) -> str:
    return name.replace("_", " ")


def first_sentence(text: str) -> str:
    """The lead sentence of a longer explanation, for a one-line email row."""
    if not text:
        return text
    cut = text.find(". ")
    return text[:cut + 1] if cut != -1 else text


# ------------------------------------------------------------------------ derivations
def worst(*statuses: str) -> str:
    return min((s for s in statuses if s), key=lambda s: SEVERITY_RANK.get(s, 9), default="GREEN")


def stream_cell(stream: dict) -> dict:
    """One matrix cell: the worst of freshness/ingest, with the two numbers a reader needs."""
    fr, ing, cp = stream["freshness"], stream["ingest"], stream["checkpoint"]
    status = worst(fr["status"], ing["status"])
    if fr.get("no_data"):
        primary, secondary = "no data", "never received"
    else:
        primary = fmt_age(fr["age_h"])
        secondary = f"{fmt_compact(ing['rows_24h'])} rows/24h"
    limit = f"limit {fmt_age(fr['threshold_h'])}" if fr.get("threshold_h") else ""
    cp_note = None
    if cp.get("missing"):
        cp_note = "checkpoint missing"
    elif cp.get("age_h") is not None and cp["age_h"] > 24 * 7:
        cp_note = f"checkpoint {fmt_age(cp['age_h'])} old"
    return {"status": status, "primary": primary, "secondary": secondary, "limit": limit,
            "checkpoint_note": cp_note, "checkpoint_status": cp["status"]}


def tenant_issues(t: dict) -> list[dict]:
    """Every non-OK, non-informational row for a tenant, as one sentence each."""
    out = []
    for key, label, _ in STREAMS:
        s = t["streams"][key]
        fr, ing, dow, cp = s["freshness"], s["ingest"], s["dow"], s["checkpoint"]
        if fr["status"] != "GREEN":
            if fr.get("no_data"):
                text = "no rows ever received"
            else:
                text = f"last row {fmt_ts(fr['last_seen'])} UTC ({fmt_age(fr['age_h'])} ago"
                text += f", limit {fmt_age(fr['threshold_h'])})" if fr.get("threshold_h") else ")"
            out.append({"status": fr["status"], "check": f"{label} freshness", "text": text})
        if ing["status"] != "GREEN":
            out.append({"status": ing["status"], "check": f"{label} ingest",
                        "text": f"{fmt_int(ing['rows_24h'])} rows in 24h ({fmt_compact(ing['lifetime'])} lifetime)"})
        if dow["status"] != "GREEN" and "yesterday" in dow:
            out.append({"status": dow["status"], "check": f"{label} volume",
                        "text": f"yesterday {fmt_int(dow['yesterday'])} vs same-weekday median {fmt_int(dow['median'])}"})
        if cp["status"] != "GREEN":
            text = "checkpoint file missing in container" if cp.get("missing") else \
                f"DLS checkpoint {fmt_ts(cp['at'])} UTC ({fmt_age(cp['age_h'])} old)"
            out.append({"status": cp["status"], "check": f"{label} checkpoint", "text": text})
    p = t["pull"]
    if p["status"] != "GREEN":
        out.append({"status": p["status"], "check": "API pull",
                    "text": f"last completed {fmt_ts(p['completed_at'])} UTC ({fmt_age(p['age_h'])} ago)"})
    d = t["db"]["drift"]
    if not d["ok"]:
        out.append({"status": d["status"], "check": "Schema drift",
                    "text": f"{d['tables']} tables missing merge columns, {fmt_int(d['rows_stuck'])} rows stuck in staging"})
    q = t["quality"]
    if q["status"] != "GREEN":
        shown = ", ".join(q["gap_dates_shown"][:5])
        out.append({"status": q["status"], "check": "Gaps (30d)",
                    "text": f"{q['gap_days_30d']} days with no data. Includes {shown}"})
    ce = t["container_errors"]
    if ce["status"] != "GREEN":
        out.append({"status": ce["status"], "check": "Container log",
                    "text": f"{ce['lines']} ERROR lines in the last {ce['window']} log lines"})
    for k, v in t["slo"].items():
        if v["status"] in ("RED", "AMBER"):
            window = f" ({v['window'][0]} to {v['window'][1]})" if v.get("window") else ""
            out.append({"status": v["status"], "check": f"SLO {humanize_check(k)}",
                        "text": f"{v['pct']:.1f}% over {v['samples']} daily samples{window}"})
    c = t["cost"]
    if c["status"] != "GREEN":
        out.append({"status": c["status"], "check": "Telephony credits",
                    "text": f"{fmt_int(c['credits_24h'])} credits across {fmt_int(c['telephony_txns_24h'])} transactions in 24h"})
    sec = t["security"]
    if sec["bypass_status"] != "GREEN":
        out.append({"status": sec["bypass_status"], "check": "Bypass codes",
                    "text": f"{sec['bypass_codes']} unexpired (threshold {sec['bypass_threshold']})"})
    out.sort(key=lambda r: SEVERITY_RANK[r["status"]])
    return out


def derive_actions(model: dict) -> list[dict]:
    """Group failing checks by root cause so the reader gets a short, ordered queue."""
    actions = []
    tenants = model["tenants"]

    # 1. One action per tenant with a broken stream: stalled (had data, stopped) or never bootstrapped.
    for t in tenants:
        if t["offboarded"]:
            continue
        stalled, never = [], []
        for key, label, _ in STREAMS:
            s = t["streams"][key]
            if s["freshness"].get("no_data"):
                never.append((key, label, s))
            elif worst(s["freshness"]["status"], s["ingest"]["status"]) in ("RED", "AMBER"):
                stalled.append((key, label, s))
        drift = t["db"]["drift"]
        if not stalled and not never and drift["ok"]:
            continue
        sev = worst(*(worst(s["freshness"]["status"], s["ingest"]["status"]) for _, _, s in stalled + never),
                    drift["status"] if not drift["ok"] else "GREEN")
        auth = t["streams"]["auth"]
        auth_flowing = auth["freshness"]["status"] == "GREEN" and auth["ingest"]["rows_24h"] > 0
        parts, cmds = [], []
        if never:
            parts.append(f"{', '.join(l for _, l, _ in never)} never delivered a row and the checkpoint files do not exist in the container, so DLS has not completed a first sync for {'these streams' if len(never) > 1 else 'this stream'}.")
        if stalled:
            names = ", ".join(l for _, l, _ in stalled)
            if auth_flowing:
                parts.append(f"{names} stopped arriving while auth is still flowing, so the container is alive but {'these streams are' if len(stalled) > 1 else 'this stream is'} dead.")
            else:
                parts.append(f"{names} stopped arriving.")
            stale = [(l, s["checkpoint"]["age_h"]) for _, l, s in stalled if s["checkpoint"].get("age_h") and s["checkpoint"]["age_h"] > 24 * 7]
            if stale:
                parts.append("DLS checkpoints are old (" + ", ".join(f"{l} {fmt_age(h)}" for l, h in stale) +
                             "), so DLS has not written those streams in a long time and the API pull has been the real source.")
            else:
                parts.append("Checkpoints match the last rows, so this is a recent stall, not a long-dead stream.")
        if not drift["ok"]:
            parts.append(f"Staging tables are missing the merge columns, so {fmt_int(drift['rows_stuck'])} rows are stuck and nothing lands until the drift fix runs.")
            if drift.get("fix"):
                cmds.append({"cmd": drift["fix"], "note": "first: unblock the merge"})
        if never and not stalled:
            cmds.append({"cmd": RUNBOOK["dls_recreate"].format(slug=t["slug"]), "note": None})
            cmds.append({"cmd": f"docker logs --tail 200 {t['slug']}-duologsync", "note": "look for the auth or API error that stopped the first sync"})
        else:
            cmds.append({"cmd": RUNBOOK["dls_restart"].format(slug=t["slug"]), "note": None})
            cmds.append({"cmd": RUNBOOK["dls_recreate"].format(slug=t["slug"]), "note": "only if the restart does not clear it"})
        api_streams = [k for k, _, _ in stalled + never if k != "admin"]
        if api_streams:
            cmds.append({"cmd": RUNBOOK["pull_stream"].format(slug=t["slug"], streams=", ".join(api_streams)), "note": "backfill while DLS catches up"})
        if any(k == "admin" for k, _, _ in stalled + never):
            cmds.append({"cmd": RUNBOOK["pull_admin"].format(slug=t["slug"]), "note": None})
        order = [k for k, _, _ in STREAMS]
        broken = [l for k, l, _ in sorted(stalled + never, key=lambda x: order.index(x[0]))]
        if never and len(never) >= 3 and not stalled:
            title = f"{t['name']}: pipeline never bootstrapped"
        elif not broken:
            title = f"{t['name']}: schema drift blocking merges"
        else:
            title = f"{t['name']}: {', '.join(broken)} {'stalled' if not never else 'not flowing'}"
        actions.append({"severity": sev, "group": "stream", "nonprod": t["nonprod"], "title": title,
                        "why": " ".join(parts), "tenants": [t["slug"]], "commands": cmds})

    # 2. API fallback pull not running.
    late = [t for t in tenants if not t["offboarded"] and t["pull"]["age_h"] is not None and t["pull"]["age_h"] > 24]
    if late:
        actions.append({"severity": "AMBER", "group": "pull",
                        "title": f"API pull has not completed in over 24h for {len(late)} tenants",
                        "why": "The scheduled pull is the safety net when DLS drops a stream. " +
                               "; ".join(f"{t['name']} last ran {fmt_age(t['pull']['age_h'])} ago" for t in late) +
                               ". The report's scheduled-task check found zero tasks and nssm is not on PATH, so the report cannot see the scheduler on this host: verify it directly.",
                        "tenants": [t["slug"] for t in late], "nonprod": False,
                        "commands": [{"cmd": "schtasks /Query /FO LIST /V | findstr /I \"Duo-Analytics\"", "note": "on the report host"},
                                     {"cmd": f"nssm status {model['fleet']['infra'].get('service_name', 'Duo-Analytics')}", "note": None}]})

    # 3. Containers restarted moments before the run: the GREEN is not yet earned.
    fresh = [c for c in model["fleet"]["infra"]["containers"] if c["health"] != "healthy"]
    if fresh:
        actions.append({"severity": "INFO", "group": "infra", "nonprod": False,
                        "title": f"{len(fresh)} containers restarted just before this run",
                        "why": ", ".join(f"{c['name']} up {c['uptime']}" for c in fresh) +
                               ". Health is still 'starting', so today's freshness numbers for these tenants predate the restart. Re-check after the next run before acting on them again.",
                        "tenants": [c["tenant"] for c in fresh], "commands": []})

    # 4. Observability gaps in the report itself.
    active = [t for t in tenants if not t["offboarded"]]
    warming = [t for t in active if all(v.get("warming") for v in t["slo"].values())]
    if len(warming) >= max(2, len(active) // 2):
        actions.append({"severity": "INFO", "group": "report", "nonprod": False,
                        "title": f"SLO history exists for only {len(active) - len(warming)} of {len(active)} tenants",
                        "why": "Every other tenant reports 0 samples. The sampler is either not scheduled per tenant or not persisting, so SLO percentages cannot be trusted fleet-wide yet.",
                        "tenants": [t["slug"] for t in warming], "commands": []})

    # Customers before non-production tenants at equal severity; then streams before everything else.
    actions.sort(key=lambda a: (SEVERITY_RANK[a["severity"]], a["nonprod"], a["group"] != "stream"))
    for i, a in enumerate(actions, 1):
        a["n"] = i
    return actions


def retire_deprecated(model: dict) -> int:
    """Remove deprecated-stream checks from the collector's counts. Returns how many were retired."""
    s = model["summary"]
    retired = 0
    for t in model["tenants"]:
        for key in DEPRECATED_STREAMS:
            st = t["streams"].get(key)
            if not st:
                continue
            for part in ("freshness", "ingest", "dow", "checkpoint"):
                status = st[part]["status"]
                bucket = {"RED": "err", "AMBER": "warn", "GREEN": "ok"}.get(status)
                if bucket:
                    s[bucket] = max(0, s[bucket] - 1)
                retired += 1
    s["retired"] = retired
    return retired


def enrich(model: dict) -> dict:
    tenants = model["tenants"]
    retire_deprecated(model)
    for t in tenants:
        t["offboarded"] = is_offboarded(t)
        t["nonprod"] = is_nonprod(t)
        t["cells"] = {k: stream_cell(t["streams"][k]) for k, _, _ in STREAMS}
        t["issues"] = tenant_issues(t)
        # Status is recomputed from what is left once deprecated checks are gone.
        t["status"] = worst(*(i["status"] for i in t["issues"] if i["status"] in ("RED", "AMBER")))
        t["err"] = sum(1 for i in t["issues"] if i["status"] == "RED")
        t["warn"] = sum(1 for i in t["issues"] if i["status"] == "AMBER")
        t["headline"] = "; ".join(f"{i['check']} {i['text']}" for i in t["issues"][:2]) or "all checks OK"
        t["pull_cell"] = {"status": t["pull"]["status"], "primary": fmt_age(t["pull"]["age_h"]),
                          "secondary": (f"+{fmt_compact(t['pull']['auth_rows'])} auth / +{fmt_compact(t['pull']['tel_rows'])} tel"
                                        if t["pull"].get("auth_rows") is not None else "")}
        d = t["db"]["drift"]
        t["drift_cell"] = {"status": d["status"], "primary": "parity" if d["ok"] else f"{fmt_compact(d['rows_stuck'])} stuck",
                           "secondary": "" if d["ok"] else f"{d['tables']} tables"}
        healthy = [f"{label.lower()} stream" for key, label, _ in STREAMS if t["cells"][key]["status"] == "GREEN"]
        if d["ok"]:
            healthy.append("staging parity")
        if t["pull"]["status"] == "GREEN":
            healthy.append("API pull")
        if t["container_errors"]["status"] == "GREEN":
            healthy.append("container log")
        if t["cost"]["status"] == "GREEN":
            healthy.append("telephony spend")
        if t["security"]["bypass_status"] == "GREEN":
            healthy.append("bypass codes")
        if t["quality"]["status"] == "GREEN":
            healthy.append("no data gaps")
        t["healthy"] = healthy
        t["change_summary"] = ", ".join(f"{k} {v}" for k, v in sorted(t["changes"]["change_log"].items()))
    model["offboarded_tenants"] = [t for t in tenants if t["offboarded"]]
    active_tenants = [t for t in tenants if not t["offboarded"]]
    model["actions"] = derive_actions(model)
    model["customer_tenants"] = [t for t in active_tenants if not t["nonprod"]]
    model["nonprod_tenants"] = [t for t in active_tenants if t["nonprod"]]
    customer_tenants = model["customer_tenants"]
    s = model["summary"]
    s["total"] = s["ok"] + s["warn"] + s["err"]
    # Red/amber/ok tenant counts and the fleet-wide verdict are scoped to
    # customer-facing tenants only. A lab or non-production tenant can still
    # fill the action queue with real work, but it never turns the headline
    # red for a customer nobody it affects.
    s["tenants_red"] = sum(1 for t in customer_tenants if t["status"] == "RED")
    s["tenants_amber"] = sum(1 for t in customer_tenants if t["status"] == "AMBER")
    s["tenants_green"] = sum(1 for t in customer_tenants if t["status"] == "GREEN")
    s["nonprod_tenants"] = len(model["nonprod_tenants"])
    s["nonprod_red"] = sum(1 for t in model["nonprod_tenants"] if t["status"] == "RED")
    s["nonprod_amber"] = sum(1 for t in model["nonprod_tenants"] if t["status"] == "AMBER")
    r = model["report"]
    r["overall"] = worst(*(t["status"] for t in customer_tenants)) if customer_tenants \
        else worst(*(t["status"] for t in tenants))
    ts = datetime.fromisoformat(r["generated_at"].replace("Z", "+00:00"))
    r["date_long"] = ts.strftime("%A %-d %B %Y")
    r["date_short"] = ts.strftime("%b %d, %Y")
    r["time_utc"] = ts.strftime("%H:%M UTC")
    needing_action = s["tenants_red"] + s["tenants_amber"]
    if needing_action:
        r["subject"] = (f"Duo Analytics [{sev_label(r['overall'])}] "
                        f"{needing_action} customer tenant{'s' if needing_action != 1 else ''} "
                        f"need action — {r['date_short']}")
    else:
        r["subject"] = f"Duo Analytics [OK] All customer tenants healthy — {r['date_short']}"
    # Condensed for the email body: customer-affecting actions first (the
    # things this alert exists to report), lab/non-prod actions after under
    # their own count, informational items dropped to a single footnote.
    actionable = [a for a in model["actions"] if a["severity"] in ("RED", "AMBER")]
    model["email_rows"] = [a for a in actionable if not a["nonprod"]]
    model["email_rows_nonprod"] = [a for a in actionable if a["nonprod"]]
    model["email_info_count"] = len(model["actions"]) - len(actionable)
    return model


# -------------------------------------------------------------------------- anonymize
def anonymize(model: dict) -> dict:
    """Replace tenant identity with neutral labels so a rendered sample can be shared."""
    mapping = {}
    prod_i = lab_i = 0
    for t in model["tenants"]:
        if t["lab"]:
            lab_i += 1
            new = f"lab-{chr(96 + lab_i)}"
            t["name"] = f"Lab Tenant {chr(64 + lab_i)} ({t['edition'].replace('Duo ', '')})"
        else:
            prod_i += 1
            new = f"tenant-{chr(96 + prod_i)}"
            t["name"] = f"Customer {chr(64 + prod_i)}" + (" (Non-Production)" if "Non-Production" in t["name"] else "")
        mapping[t["slug"]] = new
        t["slug"] = new
        t["account"] = f"ACCT{prod_i + lab_i:02d}"
        t["frameworks"] = t["frameworks"][:3]
        t["security"]["users"] = round(t["security"]["users"], -2) if t["security"]["users"] else t["security"]["users"]
        t["cost"]["active_users"] = round(t["cost"]["active_users"], -2) if t["cost"]["active_users"] else t["cost"]["active_users"]
        d = t["db"]["drift"]
        if d.get("fix"):
            d["fix"] = RUNBOOK["drift_fix"].format(slug=new)
    for c in model["fleet"]["infra"]["containers"]:
        c["tenant"] = mapping.get(c["tenant"], c["tenant"])
        c["name"] = f"{c['tenant']}-duologsync"
    model["fleet"]["infra"]["host"] = "REPORT-HOST"
    model["fleet"]["infra"]["service_name"] = "Duo-Analytics"
    if model["fleet"]["infra"].get("scheduler"):
        model["fleet"]["infra"]["scheduler"]["note"] = "collector found 0 scheduled tasks"
    if model["fleet"]["infra"].get("nssm"):
        model["fleet"]["infra"]["nssm"]["note"] = "nssm not on PATH"
    model["report"]["run_id"] = "20260906T110000Z-sample"
    return model


# ------------------------------------------------------------------------------- main
TEMPLATES = {"email": "daily_status_email.html.j2", "full": "daily_status.html.j2"}


def render(model: dict, anonymize_output: bool = False, style: str = "email") -> str:
    if anonymize_output:
        model = anonymize(model)
    model = enrich(model)
    env = Environment(loader=FileSystemLoader(HERE / "templates"), autoescape=True,
                      trim_blocks=True, lstrip_blocks=True)
    env.filters.update({"age": fmt_age, "int": fmt_int, "compact": fmt_compact, "mb": fmt_mb, "ts": fmt_ts,
                        "duration": fmt_duration, "sev": sev_label, "glyph": sev_glyph, "human": humanize_check,
                        "first_sentence": first_sentence})
    env.globals["STREAMS"] = STREAMS
    env.globals["DEPRECATED"] = [label for key, label, _ in ALL_STREAMS if key in DEPRECATED_STREAMS]
    env.globals["RUNBOOK"] = RUNBOOK
    return env.get_template(TEMPLATES[style]).render(m=model)


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("model", type=Path, help="fleet-status JSON")
    ap.add_argument("-o", "--out", type=Path, required=True, help="output HTML path")
    ap.add_argument("--anonymize", action="store_true", help="replace tenant identity with neutral labels")
    ap.add_argument("--style", choices=sorted(TEMPLATES), default="email",
                    help="email: plain, terse, table-based, matches a Duo admin alert (default, this is what gets mailed). "
                         "full: the dashboard — action queue, stream matrix, tenant detail — for a linked web view.")
    args = ap.parse_args()
    model = json.loads(args.model.read_text())
    args.out.write_text(render(model, args.anonymize, args.style))
    print(f"wrote {args.out} ({args.out.stat().st_size // 1024} KB, style={args.style})")


if __name__ == "__main__":
    main()
