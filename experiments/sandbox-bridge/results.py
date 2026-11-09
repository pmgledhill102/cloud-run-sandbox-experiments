#!/usr/bin/env python3
"""Fetch one rig execution's logs, save them, and print a summary.

  PROJECT=<id> python3 results.py EXECUTION [EXECUTION...]

Saves results/<date>-<suffix>.jsonl per execution (one JSON object per log
entry, with its timestamp as _ts), then prints one row per check with the
outcome in each context, so runs can be compared side by side.
"""
import json
import os
import subprocess
import sys

PROJECT = os.environ["PROJECT"]
HERE = os.path.dirname(os.path.abspath(__file__))


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
    day = rows[0]["_ts"][:10]
    path = os.path.join(HERE, "results", f"{day}-{execution.rsplit('-', 1)[-1]}.jsonl")
    with open(path, "w") as f:
        for r in rows:
            f.write(json.dumps(r) + "\n")
    return path, rows


def main():
    table, ctxs, notes = {}, [], []
    for ex in sys.argv[1:]:
        path, rows = fetch(ex)
        notes.append(f"{ex}: {len(rows)} entries -> {os.path.relpath(path, HERE)}")
        for r in rows:
            comp = r.get("component")
            if comp == "tests":
                ctx = f"{r.get('ctx')}"
                if ctx not in ctxs:
                    ctxs.append(ctx)
                cell = r.get("outcome", "")
                if cell == "info":
                    cell = str(r.get("detail", ""))[:40]
                table.setdefault(r["check"], {}).setdefault(ctx, []).append(cell)
            elif comp == "channels":
                table.setdefault("channel:" + r.get("probe", ""), {}).setdefault(
                    "-", []).append(str(r.get("result", ""))[:40])
            elif comp == "launcher" and any(k in r.get("msg", "") for k in
                                              ("versions:", "file-s2h", "hosts", "exited")):
                notes.append(f"{ex}: {r['msg'][:140]}")
    for n in notes:
        print(n)
    cols = ctxs + ["-"]
    print("check\t" + "\t".join(cols))
    for check, by in table.items():
        print(check + "\t" + "\t".join(" / ".join(sorted(set(by.get(c, [])))) for c in cols))


if __name__ == "__main__":
    main()
