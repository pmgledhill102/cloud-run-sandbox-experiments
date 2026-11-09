#!/usr/bin/env python3
"""Fetch one POC execution's logs, save them, and print a summary.

  PROJECT=<id> python3 results.py EXECUTION [EXECUTION...]

Saves results/<date>-<suffix>.jsonl (one log entry per line, timestamp as
_ts). Refuses to write if any entry contains an Anthropic key or a Google
access token shape, so a leak in the logs can't become a leak in git. Then
prints the attack probe as a table, one column per context, and the gateway
and agent lines.
"""
import json
import os
import re
import subprocess
import sys

PROJECT = os.environ["PROJECT"]
HERE = os.path.dirname(os.path.abspath(__file__))
SECRET = re.compile(r"sk-ant-[A-Za-z0-9_-]{8,}|ya29\.[A-Za-z0-9_-]{8,}")


def fetch(execution):
    flt = ('resource.type="cloud_run_job" AND '
           f'labels."run.googleapis.com/execution_name"="{execution}"')
    out = subprocess.run(
        ["gcloud", "logging", "read", flt, "--project", PROJECT, "--order", "asc",
         "--limit", "5000", "--format", "json"],
        check=True, capture_output=True, text=True).stdout
    rows = []
    for e in json.loads(out):
        j = e.get("jsonPayload") or {"text": e.get("textPayload", "")}
        j["_ts"] = e.get("timestamp")
        rows.append(j)
    if not rows:
        sys.exit(f"{execution}: no log entries (logs can lag a minute; retry)")
    text = "\n".join(json.dumps(r) for r in rows)
    if SECRET.search(text):
        sys.exit(f"{execution}: a secret-shaped value is in the logs; not saving. Investigate in Cloud Logging.")
    day = rows[0]["_ts"][:10]
    path = os.path.join(HERE, "results", f"{day}-{execution.rsplit('-', 1)[-1]}.jsonl")
    with open(path, "w") as f:
        f.write(text + "\n")
    return path, rows


def summary(rows):
    attacks = {}
    for r in rows:
        t = r.get("text", "")
        if t.startswith("ATTACK "):
            a = json.loads(t[7:])
            attacks.setdefault(a["ctx"], a)  # first report per context
    if attacks:
        ctxs = list(attacks)
        keys = [k for k in attacks[ctxs[0]] if k not in ("component", "ctx")]
        print("| check | " + " | ".join(ctxs) + " |")
        print("| --- |" + " --- |" * len(ctxs))
        for k in keys:
            cells = []
            for c in ctxs:
                v = attacks[c].get(k, "")
                cells.append(str(len(v)) + " found" if isinstance(v, list) else str(v))
            print(f"| {k} | " + " | ".join(cells) + " |")
    for r in rows:
        comp = r.get("component", "")
        t = r.get("text", "")
        if comp in ("launcher", "agent.sh") or "model-gateway" in t or comp == "model-gateway":
            print(r.get("_ts", "")[11:19], comp or "", r.get("msg", t)[:240])


def main():
    for ex in sys.argv[1:]:
        path, rows = fetch(ex)
        print(f"## {ex}: {len(rows)} entries -> {os.path.relpath(path, HERE)}")
        summary(rows)


if __name__ == "__main__":
    main()
