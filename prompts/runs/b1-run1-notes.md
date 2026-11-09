# b1: Cloud Run sandbox default network isolation

Date: 2026-11-07 (UTC), started 21:05, finished about 21:25.
Project `<sandbox-project>`, region `europe-west1`.

## Docs read (2026-11-07)

| Doc | URL |
| --- | --- |
| Code execution in Cloud Run (sandboxes) | https://docs.cloud.google.com/run/docs/code-execution |
| Configure sandboxes for jobs | https://docs.cloud.google.com/run/docs/configuring/jobs/sandboxes |
| Cloud Run sandbox CLI reference | https://docs.cloud.google.com/run/docs/reference/sandbox-cli |
| Cloud Run Admin API discovery doc (v2, revision 20261106) | https://run.googleapis.com/$discovery/rest?version=v2 |

All three pages are marked **Preview**.

## Versions

- gcloud SDK 570.0.0, beta component 2026.05.22
- Image: `python:3.12.7-slim-bookworm` + Debian `curl`; built by Cloud Build
- Sandbox kernel reported `4.19.0-gvisor`; host container kernel `6.9.12`

## How sandboxes were enabled on the job

The docs say `gcloud beta run jobs create JOB --sandbox-launcher`. **gcloud 570
has no such flag** in either `run jobs create` or `beta run jobs create`
(`--help | grep -i sandbox` returns nothing). The Admin API v2 has
`GoogleCloudRunV2Container.sandboxLauncher` (boolean), so the job is created
over REST by `runapi.py` with `launchStage: BETA` and
`containers[0].sandboxLauncher: true` (`job.json`). The live definition read
back is `job-live.json` and shows `"sandboxLauncher": true`.

## Doc claims and where each is checked

| Claim (source) | Check |
| --- | --- |
| Outbound network blocked by default (code-execution, CLI ref) | C1-C4 in `sandbox` |
| `--allow-egress` lets code reach external hosts and VPC endpoints the resource can reach | C1-C4 in `sandbox-allow-egress` |
| No access to the metadata server by default / no metadata credentials | C5 (project-id only, never a token) |
| No access to the parent workload | C6 (host loopback listener) |
| Env vars not inherited from host | C7 |
| Host root fs mounted read-only at `/`, only `/tmp` writable | C8 (`/proc/mounts` for `/`) |
| Processes run "with sudo privileges as a non-root user" | C8 `id -u` - **contradicted: uid 0** |
| Sandboxes isolated from each other | out of scope (one sandbox at a time) |
| `--write`, `--mount`, tar snapshots, fork | out of scope (filesystem features, not isolation defaults) |
| Shares host CPU/memory | out of scope |
| Lifecycle events logged to Cloud Logging | observed incidentally: `[start]`/`[end]` lines appear |

## Design

- `image/run.py` (job entrypoint) starts an HTTP listener on host
  `127.0.0.1:18081`, then runs `image/probe.py` three times in the same
  execution: `sandbox do` (no flags beyond `--stdin=false --sandbox-name`),
  `sandbox do --allow-egress`, and directly on the host. It logs each argv, so
  each execution shows which context had `--allow-egress` (P5).
- `B1_HOST_MARKER` is set on the job's container env (non-secret value).
- Each network check runs with curl (`--noproxy '*'`) and with Python's
  socket/urllib (proxy handler disabled). C7 uses Python and `printenv`; C8
  Python and `id`/`uname`/`grep`.
- C1 pass = name resolved (curl got past resolution / `getaddrinfo` answered).
  C2 and C5 pass = HTTP 200. C3/C4 pass = TCP connect completed (curl records a
  connect time; TLS to 1.1.1.1 also completed with a 301).
- Order within each execution: sandbox, sandbox-allow-egress, host.

## Can the checks fail? Can they pass?

- Every network check produced both a PASS (host) and a FAIL (sandbox) within
  the same execution, with the same probe code, so none is stuck either way.
- C1-C3 passing under `--allow-egress` proves the sandbox runtime can carry
  those checks to success; their failure in `sandbox` is the isolation, not
  the probe.
- During development the probe was also run in this agent session, where DNS
  is absent: C1/C2 failed there too, a further demonstration the checks speak.
- `summarize.py`'s repeatability comparison was fed a doctored copy of one
  execution (C3 sandbox curl flipped to PASS) and reported `DIFFERS`, so its
  "identical" verdict is not silence.

## Results

Executions: `b1-isolation-9c2pn`, `b1-isolation-vzww2`, `b1-isolation-9j45c`
(each 1/1 succeeded). Tables produced by `summarize.py` from Cloud Logging
(`logs/*.json`).

### b1-isolation-9c2pn

- `sandbox`: `/usr/local/gcp/bin/sandbox do --stdin=false --sandbox-name b1-sb-9c2pn -- /usr/local/bin/python3 /app/probe.py sandbox 18081`
- `sandbox-allow-egress`: `/usr/local/gcp/bin/sandbox do --stdin=false --allow-egress --sandbox-name b1-sbe-9c2pn -- /usr/local/bin/python3 /app/probe.py sandbox-allow-egress 18081`
- `host`: `/usr/local/bin/python3 /app/probe.py host 18081`

| Check | Client | sandbox | sandbox-allow-egress | host |
| --- | --- | --- | --- | --- |
| C1 | curl | FAIL - no DNS (could not resolve host) | PASS - resolved (curl got past name resolution) | PASS - resolved (curl got past name resolution) |
| C1 | python | FAIL - no DNS (Temporary failure in name resolution) | PASS - ok | PASS - ok |
| C2 | curl | FAIL - no DNS (could not resolve host) | PASS - ok | PASS - ok |
| C2 | python | FAIL - no DNS (Temporary failure in name resolution) | PASS - ok | PASS - ok |
| C3 | curl | FAIL - could not connect (refused/unreachable) | PASS - tcp connected | PASS - tcp connected |
| C3 | python | FAIL - unreachable (ENETUNREACH) | PASS - ok | PASS - ok |
| C4 | curl | FAIL - could not connect (refused/unreachable) | FAIL - could not connect (refused/unreachable) | PASS - tcp connected |
| C4 | python | FAIL - unreachable (ENETUNREACH) | FAIL - unreachable (ENETUNREACH) | PASS - ok |
| C5-ip | curl | FAIL - could not connect (refused/unreachable) | FAIL - timeout | PASS - ok |
| C5-ip | python | FAIL - unreachable (ENETUNREACH) | FAIL - timeout | PASS - ok |
| C5-name | curl | FAIL - could not connect (refused/unreachable) | FAIL - timeout | PASS - ok |
| C5-name | python | FAIL - unreachable (ENETUNREACH) | FAIL - timeout | PASS - ok |
| C6 | curl | FAIL - could not connect (refused/unreachable) | FAIL - could not connect (refused/unreachable) | PASS - ok |
| C6 | python | FAIL - connection refused | FAIL - connection refused | PASS - ok |
| C7 | printenv | FAIL - not set | FAIL - not set | PASS - visible |
| C7 | python | FAIL - not set | FAIL - not set | PASS - visible |
| C8 | python | uid=0 kernel=4.19.0-gvisor root=none / 9p | uid=0 kernel=4.19.0-gvisor root=none / 9p | uid=0 kernel=6.9.12 root=none / overlay |
| C8 | shell | uid=0 kernel=4.19.0-gvisor root=none / 9p | uid=0 kernel=4.19.0-gvisor root=none / 9p | uid=0 kernel=6.9.12 root=none / overlay |

### b1-isolation-vzww2

- `sandbox`: `/usr/local/gcp/bin/sandbox do --stdin=false --sandbox-name b1-sb-vzww2 -- /usr/local/bin/python3 /app/probe.py sandbox 18081`
- `sandbox-allow-egress`: `/usr/local/gcp/bin/sandbox do --stdin=false --allow-egress --sandbox-name b1-sbe-vzww2 -- /usr/local/bin/python3 /app/probe.py sandbox-allow-egress 18081`
- `host`: `/usr/local/bin/python3 /app/probe.py host 18081`

| Check | Client | sandbox | sandbox-allow-egress | host |
| --- | --- | --- | --- | --- |
| C1 | curl | FAIL - no DNS (could not resolve host) | PASS - resolved (curl got past name resolution) | PASS - resolved (curl got past name resolution) |
| C1 | python | FAIL - no DNS (Temporary failure in name resolution) | PASS - ok | PASS - ok |
| C2 | curl | FAIL - no DNS (could not resolve host) | PASS - ok | PASS - ok |
| C2 | python | FAIL - no DNS (Temporary failure in name resolution) | PASS - ok | PASS - ok |
| C3 | curl | FAIL - could not connect (refused/unreachable) | PASS - tcp connected | PASS - tcp connected |
| C3 | python | FAIL - unreachable (ENETUNREACH) | PASS - ok | PASS - ok |
| C4 | curl | FAIL - could not connect (refused/unreachable) | FAIL - could not connect (refused/unreachable) | PASS - tcp connected |
| C4 | python | FAIL - unreachable (ENETUNREACH) | FAIL - unreachable (ENETUNREACH) | PASS - ok |
| C5-ip | curl | FAIL - could not connect (refused/unreachable) | FAIL - timeout | PASS - ok |
| C5-ip | python | FAIL - unreachable (ENETUNREACH) | FAIL - timeout | PASS - ok |
| C5-name | curl | FAIL - could not connect (refused/unreachable) | FAIL - timeout | PASS - ok |
| C5-name | python | FAIL - unreachable (ENETUNREACH) | FAIL - timeout | PASS - ok |
| C6 | curl | FAIL - could not connect (refused/unreachable) | FAIL - could not connect (refused/unreachable) | PASS - ok |
| C6 | python | FAIL - connection refused | FAIL - connection refused | PASS - ok |
| C7 | printenv | FAIL - not set | FAIL - not set | PASS - visible |
| C7 | python | FAIL - not set | FAIL - not set | PASS - visible |
| C8 | python | uid=0 kernel=4.19.0-gvisor root=none / 9p | uid=0 kernel=4.19.0-gvisor root=none / 9p | uid=0 kernel=6.9.12 root=none / overlay |
| C8 | shell | uid=0 kernel=4.19.0-gvisor root=none / 9p | uid=0 kernel=4.19.0-gvisor root=none / 9p | uid=0 kernel=6.9.12 root=none / overlay |

### b1-isolation-9j45c

- `sandbox`: `/usr/local/gcp/bin/sandbox do --stdin=false --sandbox-name b1-sb-9j45c -- /usr/local/bin/python3 /app/probe.py sandbox 18081`
- `sandbox-allow-egress`: `/usr/local/gcp/bin/sandbox do --stdin=false --allow-egress --sandbox-name b1-sbe-9j45c -- /usr/local/bin/python3 /app/probe.py sandbox-allow-egress 18081`
- `host`: `/usr/local/bin/python3 /app/probe.py host 18081`

| Check | Client | sandbox | sandbox-allow-egress | host |
| --- | --- | --- | --- | --- |
| C1 | curl | FAIL - no DNS (could not resolve host) | PASS - resolved (curl got past name resolution) | PASS - resolved (curl got past name resolution) |
| C1 | python | FAIL - no DNS (Temporary failure in name resolution) | PASS - ok | PASS - ok |
| C2 | curl | FAIL - no DNS (could not resolve host) | PASS - ok | PASS - ok |
| C2 | python | FAIL - no DNS (Temporary failure in name resolution) | PASS - ok | PASS - ok |
| C3 | curl | FAIL - could not connect (refused/unreachable) | PASS - tcp connected | PASS - tcp connected |
| C3 | python | FAIL - unreachable (ENETUNREACH) | PASS - ok | PASS - ok |
| C4 | curl | FAIL - could not connect (refused/unreachable) | FAIL - could not connect (refused/unreachable) | PASS - tcp connected |
| C4 | python | FAIL - unreachable (ENETUNREACH) | FAIL - unreachable (ENETUNREACH) | PASS - ok |
| C5-ip | curl | FAIL - could not connect (refused/unreachable) | FAIL - timeout | PASS - ok |
| C5-ip | python | FAIL - unreachable (ENETUNREACH) | FAIL - timeout | PASS - ok |
| C5-name | curl | FAIL - could not connect (refused/unreachable) | FAIL - timeout | PASS - ok |
| C5-name | python | FAIL - unreachable (ENETUNREACH) | FAIL - timeout | PASS - ok |
| C6 | curl | FAIL - could not connect (refused/unreachable) | FAIL - could not connect (refused/unreachable) | PASS - ok |
| C6 | python | FAIL - connection refused | FAIL - connection refused | PASS - ok |
| C7 | printenv | FAIL - not set | FAIL - not set | PASS - visible |
| C7 | python | FAIL - not set | FAIL - not set | PASS - visible |
| C8 | python | uid=0 kernel=4.19.0-gvisor root=none / 9p | uid=0 kernel=4.19.0-gvisor root=none / 9p | uid=0 kernel=6.9.12 root=none / overlay |
| C8 | shell | uid=0 kernel=4.19.0-gvisor root=none / 9p | uid=0 kernel=4.19.0-gvisor root=none / 9p | uid=0 kernel=6.9.12 root=none / overlay |

### Repeatability

- All 54 (check, client, context) outcomes identical across 3 executions (ok flag and failure mode; C8 compared on full detail).

## How the failures happen (from the `info` record each probe writes)

- `sandbox`: only `lo` exists (`/proc/net/if_inet6` lists only `::1`;
  `/proc/net/route` is empty). `resolv.conf` names `169.254.169.254`, which is
  unreachable, so DNS fails with `EAI_AGAIN` ("Temporary failure in name
  resolution"); every IP connect fails immediately with `ENETUNREACH` (curl
  exit 7, 0 ms). The loopback is the sandbox's own: host `127.0.0.1:18081`
  gives `ECONNREFUSED`. None of these is a timeout.
- `sandbox-allow-egress`: an `eth0` with `172.20.0.x`, default route via
  `172.20.0.1`, IPv4 only (link-local `fe80::` only, no IPv6 route, so C4 is
  `ENETUNREACH`). DNS to `169.254.169.254` works, but HTTP to the metadata
  server at the same address **times out** (5 s) by IP and by name. Host
  loopback still refused, host env still absent.
- `host`: `ipvlan-eth0` with a ULA IPv6 address; IPv4 and IPv6 egress,
  metadata server answers `<sandbox-project>`, listener and env visible. That
  the host reaches everything directly is why an egress proxy placed in the
  host container is only advisory for the host itself: nothing below it forces
  traffic through the proxy. Inside the default sandbox no route exists at all.

## Spend

One Cloud Build build (~30 s), 3 job executions of ~1 min at
2 vCPU / 2 GiB, a few MB in Artifact Registry and GCS for minutes. Well under
GBP 0.10.

## Files

- `image/Dockerfile`, `image/run.py`, `image/probe.py` - image source
- `cloudbuild.yaml` - build config (logging NONE: build SA has no project role)
- `job.json` - job definition sent to the API; `job-live.json` - as read back
- `runapi.py` - REST helper (reads the token file in-process)
- `create.sh` - recreates repo, bucket, SAs, image, job (idempotent)
- `execute.sh` - runs N executions; `fetchlogs.sh`, `summarize.py` - results
- `cleanup.sh` - removes everything `create.sh` made; run, output in `cleanup.log`
