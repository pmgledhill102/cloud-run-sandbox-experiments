#!/usr/bin/env python3
"""Minimal Cloud Run Admin API client for the sandbox-bridge rig, adapted from the runner spike.

The installed gcloud predates the Preview sandboxLauncher field and rejects it
client-side, so jobs are created through the v1 API, which takes the same YAML
shape as `gcloud run jobs replace`. The access token is read from the broker's
token file and never appears on a command line.

  crun.py apply JOB_YAML              create or replace the job
  crun.py run JOB [ARG...]            start an execution with these container args
  crun.py get-execution EXECUTION     print the execution's timestamps and status
  crun.py cancel EXECUTION            cancel a running execution
"""
import json
import os
import sys
import time
import urllib.error
import urllib.request

PROJECT = os.environ["PROJECT"]
REGION = os.environ.get("REGION", "europe-west1")
TOKEN_FILE = os.path.expanduser("~/.config/claude/credential-broker/access_token")


def call(method, url, body=None):
    # Every URL here is built from a fixed https:// API base; refuse anything else.
    if not url.startswith("https://"):
        raise ValueError("refusing non-https URL")
    tok = open(TOKEN_FILE).read().strip()
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method, headers={
        "Authorization": "Bearer " + tok, "Content-Type": "application/json"})
    try:
        # nosemgrep: python.lang.security.audit.dynamic-urllib-use-detected.dynamic-urllib-use-detected
        with urllib.request.urlopen(req, timeout=60) as r:
            return r.status, json.load(r)
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.load(e)
        except Exception:
            return e.code, {}


def apply(path):
    import yaml  # PyYAML ships with gcloud's bundled python; fall back below
    job = yaml.safe_load(open(path))
    name = job["metadata"]["name"]
    base = f"https://{REGION}-run.googleapis.com/apis/run.googleapis.com/v1/namespaces/{PROJECT}/jobs"
    job["metadata"]["namespace"] = PROJECT
    s, existing = call("GET", f"{base}/{name}")
    if s == 200:
        job["metadata"]["resourceVersion"] = existing["metadata"]["resourceVersion"]
        s, out = call("PUT", f"{base}/{name}", job)
    else:
        s, out = call("POST", base, job)
    print(s, json.dumps(out.get("status", out), indent=1)[:1500])
    return 0 if s in (200, 201) else 1


def run(job, args):
    url = f"https://run.googleapis.com/v2/projects/{PROJECT}/locations/{REGION}/jobs/{job}:run"
    # RUN_ENV="K=V K2=V2" adds per-execution environment overrides.
    env = [{"name": k, "value": v} for k, v in (kv.split("=", 1) for kv in os.environ.get("RUN_ENV", "").split() if "=" in kv)]
    over = {}
    if args:
        over["args"] = args
    if env:
        over["env"] = env
    body = {"overrides": {"containerOverrides": [over]}} if over else {}
    t0 = time.time()
    s, op = call("POST", url, body)
    if s != 200:
        print(s, json.dumps(op)[:1500])
        return 1
    ex = op.get("metadata", {}).get("name", "")
    print(json.dumps({"called_at": t0, "operation": op.get("name"), "execution": ex}))
    return 0


def get_execution(name):
    if not name.startswith("projects/"):
        name = f"projects/{PROJECT}/locations/{REGION}/jobs/{name.rsplit('-', 1)[0]}/executions/{name}"
    s, ex = call("GET", f"https://run.googleapis.com/v2/{name}")
    keep = {k: ex.get(k) for k in ("createTime", "startTime", "completionTime", "succeededCount",
                                   "failedCount", "cancelledCount", "runningCount")}
    keep["conditions"] = [{"type": c.get("type"), "state": c.get("state"), "reason": c.get("reason"),
                           "message": (c.get("message") or "")[:300]} for c in ex.get("conditions", [])]
    print(json.dumps(keep, indent=1))
    return 0 if s == 200 else 1


def cancel(name):
    if not name.startswith("projects/"):
        name = f"projects/{PROJECT}/locations/{REGION}/jobs/{name.rsplit('-', 1)[0]}/executions/{name}"
    s, out = call("POST", f"https://run.googleapis.com/v2/{name}:cancel", {})
    print(s, json.dumps(out)[:300])
    return 0 if s == 200 else 1


if __name__ == "__main__":
    cmd, rest = sys.argv[1], sys.argv[2:]
    sys.exit({"apply": lambda: apply(rest[0]), "run": lambda: run(rest[0], rest[1:]),
              "get-execution": lambda: get_execution(rest[0]), "cancel": lambda: cancel(rest[0])}[cmd]())
