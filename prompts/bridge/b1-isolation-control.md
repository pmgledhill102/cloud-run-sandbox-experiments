# Prompt B1: isolation, and the `--allow-egress` control experiment

<!-- cspell:ignore Flavor proc -->

Copy everything below the line into a fresh agent session. Fill in **Inputs**
first.

---

## Goal

Show, with evidence, what a Cloud Run sandbox's default network isolation
does and doesn't provide, and that the tests you use **can fail**. Run the
same checks in three contexts:

| Context | What it is | Role |
| --- | --- | --- |
| `sandbox` | A Cloud Run sandbox with default settings (no egress) | Under test |
| `sandbox-allow-egress` | The same, started with `--allow-egress` | Negative control |
| `host` | The Cloud Run job's own container, outside any sandbox | Control |

No proxy and no bridge in this prompt: this is about the platform alone.

## Inputs

- **Google Cloud project:** `<PROJECT_ID>`, region `<REGION>`. You have
  `roles/owner`. A spend cap applies; stay well inside it.
- No other credential is needed.

## Before you build

1. Read the current Cloud Run documentation on sandboxes (code execution),
   on enabling them for **jobs**, and the `sandbox` CLI reference. Record each
   URL and today's date.
2. Find out how to enable sandboxes on a job with the tools you have. If
   `gcloud` lacks the setting, use the Cloud Run Admin API. Record which.
3. List what the docs claim about network, metadata server, environment and
   filesystem. Each claim becomes a check below or is marked out of scope.

## Observed on 2026-11-07: verify, don't trust

- gcloud 570.0.0 had no flag for the sandbox launcher, and `gcloud run jobs
  replace` rejected the field. The Cloud Run Admin API v2 accepted
  `sandboxLauncher: true` on the container, with launch stage `BETA`.
- The platform logs every `sandbox` command line, `--env` values included.
- Processes in the sandbox ran as uid 0, despite the docs' "non-root user".
- Cloud Build's default staging bucket is in `us`, which an EU-only location
  policy refuses: stage source in your own regional bucket, with your own
  build identity.

## Checks, in every context

Run each with **curl** and with **one non-curl client** (a short Go, Python or
Node program), so a result isn't one tool's quirk. For C7 and C8, where curl doesn't apply,
use whatever tool reads the value.

| # | Check |
| --- | --- |
| C1 | DNS resolution of a public name. For curl, it counts as resolved if curl gets past name resolution |
| C2 | HTTPS GET to a public name |
| C3 | TCP connect to a public **IPv4** literal on 443 (no DNS). Judged on the connect, not on TLS |
| C4 | TCP connect to a public **IPv6** literal on 443 |
| C5 | Metadata server by IP (`169.254.169.254`) and by name (`metadata.google.internal`), with the `Metadata-Flavor: Google` header |
| C6 | A listener the host started on its own `127.0.0.1`: reachable from this context? |
| C7 | An environment variable the host set: visible? |
| C8 | `id -u`, `uname -r`, and `/proc/mounts` for `/` |

## Acceptance

| # | Pass if |
| --- | --- |
| P1 | In `sandbox`: C1–C5 all fail, and you can say how (no DNS, unreachable, timeout) |
| P2 | In `sandbox-allow-egress`: C1–C3 **succeed**. This is what proves C1–C3 can pass at all. Report C4 and C5 as found, without assuming |
| P3 | In `host`: C1–C5 succeed (C4 if the platform has IPv6) |
| P4 | Each check ran in all three contexts in **one** Cloud Run job execution, at least **three** executions, and you say whether every outcome repeated |
| P5 | `--allow-egress` is used only for its context, and the report shows the other contexts ran without it |

## Rules

- Describe failures in `sandbox` as **isolation**, and a success in `host` as
  the reason a proxy alone is only advisory. Don't call anything a sandbox
  escape: nothing here leaves the sandbox.
- No secret on any `sandbox` command line: the platform logs them.
- Stop after 90 minutes or about £2 of spend. If one step fails three times,
  stop guessing and report it.

## Artefacts

`NOTES.md` (docs read, versions, the results table per execution), the image
source, the job definition, a script that recreates it, and a clean-up
script you have run. Build history and log entries can't be deleted: say
what's left.

## Final report

A table: check × context, with the outcome and a log excerpt for each cell.
Then what you assumed, and what you'd change in this prompt.
