# p1: one-job GitHub runner in a no-egress Cloud Run sandbox

Date of all reading and testing: **2026-11-07**. Project `<sandbox-project>`,
region `europe-west1`, repo `<throwaway-repo>`, label
`prompt-test-1`. Everything created is prefixed `p1-`.

Evidence files are in `p1/evidence/`: Cloud Run execution logs
(`<tag>-<execution>.log`, the job's stdout/stderr from Cloud Logging) and GitHub
job logs (`gh-job-*.log`, from `GET /repos/.../actions/jobs/{id}/logs`).

## Docs read (2026-11-07)

| Topic | URL | What it settled |
| --- | --- | --- |
| Cloud Run sandboxes (code execution) | https://docs.cloud.google.com/run/docs/code-execution | `sandbox do/run/exec`, egress blocked by default, `--allow-egress`, `--mount`, `--write`, env not inherited. **Preview** |
| Sandboxes for jobs | https://docs.cloud.google.com/run/docs/configuring/jobs/sandboxes | Setting is `sandboxLauncher: true` on the container (gcloud `beta run jobs ... --sandbox-launcher`), launch stage annotation `BETA`; feature **Preview** |
| Sandbox CLI reference | https://docs.cloud.google.com/run/docs/reference/sandbox-cli | flags of `sandbox do` (`--stdin`, `--stdout`, `--sandbox-name`, `delete --force`) |
| Cloud Run jobs | https://docs.cloud.google.com/run/docs/create-jobs , https://docs.cloud.google.com/run/docs/configuring/task-timeout | task timeout, max retries |
| Container contract (shutdown/signals) | https://docs.cloud.google.com/run/docs/container-contract | jobs: on timeout or cancel the task gets SIGTERM, then SIGKILL 10 s later |
| JIT runner config API | https://docs.github.com/en/rest/actions/self-hosted-runners | `POST /repos/{o}/{r}/actions/runners/generate-jitconfig` {name, runner_group_id, labels, work_folder} -> `encoded_jit_config`; list/delete runners |
| Hosts a self-hosted runner must reach | https://docs.github.com/en/actions/reference/runners/self-hosted-runners | github.com, api.github.com, *.actions.githubusercontent.com, codeload.github.com, results-receiver..., *.blob.core.windows.net, objects(-origin).githubusercontent.com, release-assets..., ghcr.io, ... |
| Run Admin API discovery | https://run.googleapis.com/$discovery/rest?version=v2 | `GoogleCloudRunV2Container.sandboxLauncher` (bool) exists in v2 |

## Versions (all pinned)

| Thing | Version |
| --- | --- |
| gcloud | Google Cloud SDK 570.0.0, beta 2026.05.22 — **has no `--sandbox-launcher` flag** (`gcloud beta run jobs create --help \| grep -c sandbox` = 0), so jobs are created with the **Cloud Run Admin API v2 directly** (`scripts/runapi.py`, `launchStage: BETA`, `containers[0].sandboxLauncher: true`) |
| Base image | `ubuntu:24.04` @ `sha256:fe81560990fae86dc4743202b2cb4d3df98d0bcf949173cecef15ce1127ef852` (24.04.5) |
| actions/runner | 2.338.2 (latest, 2026-10-26), tarball sha256 `baebf204307bac6193480852732f0aba2ae97b6c5597c244f4d5558390147c40`, checked in the Dockerfile |
| actions/checkout | v7.0.1 @ `3d3c42e5aac5ba805825da76410c181273ba90b1` |
| apt packages | from the noble archive at build time (curl, git 2.43.0, jq, python3 3.12, ...). Not pinned to exact versions — see "assumed" below |
| Sandbox runtime | gVisor (`uname -r` = `4.19.0-gvisor` in the sandbox); host kernel 6.9.12 |
| Image for A1-A8 | `europe-west1-docker.pkg.dev/<sandbox-project>/p1-runner/p1-runner@sha256:a1fe92846bf3ec443cb431c1a08019b30476d671b1bc96c6f234130a6e11ba63` (kill tests 2 and 3 used `...@sha256:223dee0e...` and `...@sha256:82a43d21...`, which differ only in the SIGTERM path) |

## Assumptions the docs did not confirm (tested first, with `p1-probe`)

1. That gcloud can set the sandbox flag — **false** for 570.0.0; REST works.
2. Which channel crosses the boundary — unknown; probed (table below).
3. Sandbox user: docs say "sudo privileges as a non-root user". **Observed: uid 0
   (`id` = root) but `sudo` fails** (`setresuid ... Operation not permitted`), and
   root is not the owner of image files (9p `dfltuid=4294967294`).
4. That the sandbox cannot reach the metadata server — confirmed (connect fails,
   even with `--allow-egress` it times out). `metadata.google.internal` still
   *resolves* in the sandbox (169.254.169.254), but connecting fails.
5. That the sandbox CLI logs its own command line — **true**: every `sandbox do`
   prints `[start] cwd=/ "<full argv>"` to the job log. So nothing secret may be
   an argument to `sandbox` (the JIT config is sent over stdin instead).

## Boundary-channel table (evidence: `evidence/probe-run1.log`, execution `p1-probe-hg5v6`)

| Channel | Result | Detail |
| --- | --- | --- |
| Loopback TCP (host listens 127.0.0.1:18080, sandbox connects) | **Fails** | `ConnectionRefusedError` — the sandbox has its own netns/loopback |
| Unix socket on a `--mount` bind mount (host listens) | **Fails** | `ConnectionRefusedError` |
| Unix socket on host rootfs, no mount (host listens) | **Fails** | `ConnectionRefusedError` |
| Unix socket on bind mount (sandbox listens, host connects) | **Fails** | host never connects; sandbox `accept` times out |
| FIFO on bind mount, either direction | **Fails** | `PermissionError: [Errno 1] Operation not permitted` (9p mount option `disable_fifo_open`) |
| Regular files on bind mount, both directions | **Works** | `file-down` / `file-up` read back; live appends visible within 0.1 s (`snapshots=['0','01','012','0123','01234']`) |
| Sandboxed process stdin/stdout | **Works** | 16 KiB binary round trip byte-exact; interactive line echo latency ~0.1 ms after first (first 132 ms) |
| Direct network from sandbox (default) | No egress | DNS fails, `tcp 1.1.1.1:443` -> `Network is unreachable`, metadata -> unreachable |
| Direct network with `--allow-egress` | Egress | `tcp 1.1.1.1:443 OK`, curl example.com 200; metadata still times out |

**Design built on stdin/stdout**: one sandboxed process (`image/inner.py`)
listens on the sandbox's own `127.0.0.1:3128` and multiplexes every accepted
TCP connection over its stdout/stdin as frames (`image/mux.py`:
`type u8 | stream u32 | len u32 | payload`, types OPEN/DATA/CLOSE/CONFIG, a
writer thread per stream so one slow socket does not block the pipe). The host
side (`HostBridge` in `image/launcher.py`) opens one connection to the
filtering proxy per stream. The runner's own output goes to stderr, which the
sandbox CLI forwards separately, so fd 1 carries only frames. Files on a bind
mount would also work but need polling; stdio is a real byte stream.

## Architecture

```text
Cloud Run job execution (host container, has egress, has metadata)
 ├─ launcher.py   host controls (A4/A5) -> Secret Manager (job SA) -> generate-jitconfig
 ├─ proxy.py      127.0.0.1:3128 hostname allowlist; CONNECT + absolute http://
 └─ sandbox do --write --sandbox-name p1-xxxx -- python3 inner.py   (no egress)
       stdin/stdout = mux pipe          stderr = runner output -> job log
       inner.py: 127.0.0.1:3128 (sandbox loopback) -> frames -> host -> proxy
       run.sh --jitconfig <from first stdin frame>, HTTPS_PROXY=http://127.0.0.1:3128
```

- Secret: `<admin-token-secret>` is read with the job's identity
  (`p1-job`, `secretAccessor` on that one secret) via the metadata token, held
  in memory only. The JIT config is passed as the first stdin frame (not argv,
  not `--env`, not a file). A scan of all evidence for `jitconfig`, `ghp_`,
  `github_pat_`, `ghs_`, `Bearer ` finds nothing.
- Proxy rules (`image/proxy.py`): ports 80/443 only; IP literals refused;
  hostname must be on the list (exact or `*.suffix`); every resolved address
  must be `is_global` (refuses private, loopback, link-local, CGNAT, reserved);
  connects to the vetted address (no second lookup). No TLS interception.
  Default list: the GitHub runner hosts from the docs page above.
- Idle limit `P1_IDLE_SECONDS` (default 300): when reached with no job
  assigned, the launcher `DELETE`s the runner registration; a 204 means no job
  can land on it any more, then it kills the sandbox. A 422 (busy) means a job
  was just assigned, so it keeps waiting.
- SIGTERM: SIGINT the listener inside the sandbox (`sandbox exec`), try a force-cancel, deregister (best effort; 422 while busy), `sandbox delete --force`. While idle this works: `evidence/sigterm-idle-p1-runner-fvqmq.log` shows `delete runner HTTP 204`.
- After the runner exits, the launcher polls `GET /runners/{id}` until 404
  (proof of self-deregistration) and deletes it itself if still present.
- Task timeout 3600 s, `maxRetries: 0`; workflow job `timeout-minutes: 15`.
- `P1_MODE=cleanup` runs the same image to delete this experiment's offline
  runners (this session cannot call the runners API).

## Acceptance tests

| # | Result | Evidence |
| --- | --- | --- |
| A1 | **PASS** | Run 42379796326 (job 128424803239) success on runner `p1-p1-runner-mhnjf` (id 85). Execution log `evidence/A1-A5-p1-runner-mhnjf.log`: `Job probe (1) completed with result: Succeeded`, `√ Removed .runner`, `post-exit GET runner 85 HTTP 404 (404 = deregistered)`, `Container called exit(0)`; execution `completed successfully in 37.41s` |
| A2 | **PASS** | `gh-job-run1-A1-A5.log`: `A2 PASS` (curl https://api.github.com/zen through the bridge); proxy `ALLOW CONNECT api.github.com:443` |
| A3 | **PASS** | run 1: `curl: (56) CONNECT tunnel failed, response 403` / `A3 PASS (refused)`; proxy `DENY CONNECT example.com:443 - host not on allowlist` |
| A3 control | **PASS (control succeeded)** | run 42380847662, execution `p1-runner-ch8rk` with `P1_EXTRA_ALLOW=example.com,metadata.google.internal`: `A3-CONTROL PASS (allowed)` in `gh-job-run2-A3ctl.log`; proxy `ALLOW CONNECT example.com:443 -> 2606:4700:10::6814:179a`. Same run shows the private-address rule: `DENY GET http://metadata.google.internal/... - resolves to non-public address 169.254.169.254` although the name was on the list |
| A4 | **PASS** | run 1: `curl: (6) Could not resolve host: example.com` and `tcp 1.1.1.1:443 failed: OSError [Errno 101] Network is unreachable` -> `A4 PASS (no direct route)` |
| A4 control (host) | **PASS** | launcher log: `A4-HOST direct https://example.com: http=200 ip=2606:4700:10::6814:179a` |
| A5 | **PASS** | run 1: direct IP `curl: (7) Failed to connect to 169.254.169.254`, direct name `Failed to connect to metadata.google.internal`, via proxy IP `p1-proxy refused: IP literal refused http=403`, via proxy name `host not on allowlist http=403`; `metadata 200 responses: 0 of 4` |
| A5 control (host) | **PASS** | launcher log: `A5-HOST direct http://169.254.169.254/...: <sandbox-project> http=200` and same for `metadata.google.internal`; via proxy from host: both refused 403 |
| A6 | **PASS** | run 42380989372, execution `p1-runner-2b6nq` with `P1_SANDBOX_EGRESS=1` (`[start] ... sandbox do ... --allow-egress ...`): `curl --noproxy rc=0 out=200`, `tcp 1.1.1.1:443 connected` -> `A6 PASS (direct succeeds with egress)`. Metadata still unreachable directly (curl 28 timeout). Restored: egress was a per-execution override only; the job definition has no such env and every later execution's `[start]` line has no `--allow-egress` |
| A7 | **PASS** (measured) | p50 38.1 s, p90 49.5 s, n=13 — section below |
| A8 | **PASS** | run 42381168781 dispatched and cancelled before any runner existed (`completed cancelled`); execution `p1-runner-45jpr` with `P1_IDLE_SECONDS=90`: `runner-listening +5.8s`, `IDLE: no job within 90s; runner 88 deregistered (HTTP 204); stopping sandbox`, `post-exit GET runner 88 HTTP 404`, execution ended by itself (`evidence/A8idle-p1-runner-45jpr.log`) |

## A7: start-up time (13 timed runs; `scripts/analyze-a7.py 42381435346 42383070484`, output in `evidence/A7-timings.md`)

Each run: the job is already queued (matrix, `max-parallel: 1`); `runapi.py run`
records `submitted` just before `POST jobs/p1-runner:run`; the launcher prints
`STAGE <name> t=<epoch>`; GitHub's `started_at` comes from the jobs API (1 s
resolution). All clocks are wall clocks on different machines (this session,
the Cloud Run instance, GitHub).

**Result: PASS (measured).** `jobs.run` -> GitHub job `started_at`:
**p50 38.1 s, p90 49.5 s** (min 22.0, max 53.5, n=13).

| Stage | p50 | p90 | What it is |
| --- | --- | --- | --- |
| submit -> launcher process starts | 30.2 s | 36.0 s | Cloud Run: schedule, image import ("Imported container image"), "Started deployed execution in 13.8-19.6 s" |
| launcher -> JIT minted | 1.3 s | 1.7 s | host controls (0.2 s), metadata token + Secret Manager (0.15 s), `generate-jitconfig` (~0.8 s) |
| JIT minted -> "Listening for Jobs" | 4.2 s | 10.3 s | `sandbox do` start (~0.1-0.3 s to inner.py) + runner boot through the bridge. One outlier 35.5 s (A7b-4: 33 s between the bridge coming up and the runner's first proxy connection; cause not established) |
| Listening -> "Running job" | 1.8 s | 1.9 s | GitHub assigns the queued job |

Cloud Run dominates (about 80 % of the p50). The sandbox itself is cheap.

Full table:

skip p1-runner-lmplr no job
skip p1-runner-fvqmq no job
| execution | job | result | submit->container | container->jit | jit->listening | listening->running | submit->gh_started_at | submit->running(log) | job duration(gh) |
|---|---|---|---|---|---|---|---|---|---|
| p1-runner-d4kl4 | probe (1) | success | 19.0 | 1.5 | 4.3 | 1.8 | 25.8 | 26.6 | 15.0 |
| p1-runner-kd4tl | probe (2) | success | 32.7 | 1.1 | 3.9 | 2.0 | 38.9 | 39.6 | 10.0 |
| p1-runner-k7pzs | probe (3) | success | 30.5 | 6.7 | 3.3 | 1.8 | 41.6 | 42.3 | 9.0 |
| p1-runner-5qgq2 | probe (4) | success | 33.1 | 1.2 | 3.4 | 1.8 | 38.7 | 39.5 | 10.0 |
| p1-runner-hhx6z | probe (5) | success | 31.2 | 1.2 | 4.7 | 1.9 | 38.1 | 39.0 | 11.0 |
| p1-runner-6fqsn | probe (6) | success | 30.2 | 1.2 | 3.8 | 1.7 | 35.8 | 36.9 | 10.0 |
| p1-runner-vqdkr | probe (7) | success | 28.5 | 1.4 | 3.8 | 1.8 | 34.4 | 35.5 | 10.0 |
| p1-runner-kcbj5 | probe (8) | success | 28.4 | 1.4 | 4.2 | 1.8 | 34.8 | 35.8 | 11.0 |
| p1-runner-vhd8x | probe (9) | failure | 27.3 | 1.2 | 4.2 | 1.8 | 33.6 | 34.5 | 8.0 |
| p1-runner-nf9hp | probe (1) | success | 36.7 | 1.3 | 10.1 | 1.9 | 49.5 | 50.0 | 11.0 |
| p1-runner-xkxch | probe (2) | success | 15.9 | 1.4 | 3.9 | 1.7 | 22.0 | 22.9 | 11.0 |
| p1-runner-tc76n | probe (3) | success | 36.8 | 1.1 | 10.3 | 1.8 | 49.3 | 50.0 | 10.0 |
| p1-runner-2tjtk | probe (4) | success | 14.9 | 1.7 | 35.5 | 1.8 | 53.5 | 53.9 | 11.0 |

n=13
submit->container        p50=  30.2s p90=  36.0s min=  14.9s max=  36.8s
container->jit           p50=   1.3s p90=   1.7s min=   1.1s max=   6.7s
jit->listening           p50=   4.2s p90=  10.3s min=   3.3s max=  35.5s
listening->running       p50=   1.8s p90=   1.9s min=   1.7s max=   2.0s
submit->gh_started_at    p50=  38.1s p90=  49.5s min=  22.0s max=  53.5s
submit->running(log)     p50=  39.0s p90=  50.0s min=  22.9s max=  53.9s
job duration(gh)         p50=  10.0s p90=  11.0s min=   8.0s max=  15.0s

A7 notes: run 9 of the first batch **failed at A2** — `curl: (22) The requested
URL returned error: 403` while the proxy logged `ALLOW CONNECT
api.github.com:443` (so the 403 came from GitHub, not the proxy). Most likely
the unauthenticated per-IP rate limit on Cloud Run's shared egress IP (not
verified: `-f` hid the body). `fail-fast` then cancelled job 10, and that
execution (`p1-runner-lmplr`) waited out its 300 s idle limit, deregistered
(HTTP 204) and exited — an unplanned second A8 pass. The workflow now sends
`GITHUB_TOKEN` on the `/zen` call and has `fail-fast: false`.


## Differences from a GitHub-hosted runner (observed)

| Area | GitHub-hosted | This runner | Evidence / fix |
| --- | --- | --- | --- |
| User | `runner` (uid 1001) with passwordless sudo | uid 0 in gVisor, **sudo broken** (`setresuid(-1, 1, -1): Operation not permitted`) | every `gh-job-*.log` "differences" step |
| File ownership | chown works | **`chown 1001:1001 f: Operation not permitted`** | same step. Root is not even the owner of image files: the first execution failed with `cp: cannot create regular file '/opt/actions-runner/run-helper.sh': Permission denied` despite `--write` (`evidence/fail1-perm-p1-runner-qzggc.log`). Fix: `chmod -R a+rwX /opt/actions-runner /root` in the image |
| apt-get at job time | works | **fails even with egress**: `Failed to setgroups - setgroups (1: Operation not permitted)` (apt drops to `_apt`). Would additionally need archive hosts on the allowlist. Untested fix: `-o APT::Sandbox::User=root`. Install tools in the image instead |
| Network | open internet | only allowlisted hosts via `HTTPS_PROXY`; no DNS in the sandbox at all. Tools that ignore proxy env vars fail with "Could not resolve host". curl, git and the runner's node actions (checkout) honoured `https_proxy` | A2/A4 |
| Docker | available | none (no daemon on Cloud Run, not provided) | `docker: absent` |
| Disk/CPU | 4 vCPU, 16 GiB, 14 GB SSD | 5 CPUs visible (`nproc`; the quota is 2 vCPU), 3.9 GiB, `/` is the in-memory overlay (`8.0E` reported), `/tmp` 2 GiB tmpfs | differences step |
| Workspace | `/home/runner/work` | `/opt/actions-runner/_work` inside the overlay; lost when the sandbox ends | checkout log |
| Hosts used by one job | — | broker., run-actions-2-azure-eastus., pipelinesghubeus8., results-receiver., launch., token., run.actions.githubusercontent.com, productionresultssa4.blob.core.windows.net, codeload.github.com, github.com, api.github.com | proxy ALLOW lines |
| Runner labels | — | JIT runner shows only `prompt-test-1` (no `self-hosted`, `Linux`, `X64` added) | `JIT runner minted ... labels ['prompt-test-1']` |

## Known failure case: runner killed mid-job (time-boxed, not fixed)

Script `scripts/kill-test.sh`: dispatch a job with `sleep`, start an execution,
wait until GitHub says `in_progress`, wait 20 s, `gcloud run jobs executions cancel`.

| Try | Shutdown handling | GitHub job after the kill | Evidence |
| --- | --- | --- | --- |
| 1 | SIGTERM -> `DELETE runner` (got **422**, runner busy) -> `sandbox delete --force` | stayed `in_progress` for **~9.5 min**; then `completed failure` at 15:02:57, 10 m 01 s after it started | `evidence/kill-test-1-no-graceful.out`, `evidence/kill-p1-runner-lwtvj.log` |
| 2 | as 1, plus SIGINT to `Runner.Listener` and `Runner.Worker` inside the sandbox via `sandbox exec` (runner printed `Exiting...` but did not finish within 5 s) | same: `in_progress` until 15:16:01, again 10 m 01 s after start (sleep was 840 s, so this is GitHub's lost-runner timeout, not the step ending) | `evidence/kill-test-2-graceful.out` |
| 3 | SIGINT to the listener only, then look up the job's run by runner name and `POST /actions/runs/{id}/force-cancel` | no `force-cancel` line was logged: the run lookup found nothing (most likely the admin token has Administration but not Actions read; not checked, since the launcher does not log the HTTP status of the lookup); job stayed `in_progress` until 16:24:33, again **10 m 01 s** after it started | `evidence/kill-test-3-force-cancel.out` |

Conclusion: a runner killed mid-job leaves the job `in_progress` for about 10
minutes (GitHub's lost-runner detection); the registration cannot be deleted
while busy (422). Next step (not done, time box): give the dispatcher a
token with `actions:write` and force-cancel from outside the execution, or
give the runner more than Cloud Run's 10 s to report.

**Credential hygiene finding:** in try 2, `sigrunner.py` logged the first 80
bytes of the listener's command line, which includes the start of the JIT
config (24 base64 characters: the generic JSON prefix `{".runner":"eyJBZ2`,
no key material, and the runner was single-use and already gone). Fixed
in try 3 (it prints only the process name). The entry is still in Cloud
Logging for execution `p1-runner-wlskj`. Also inherent in the design: the JIT
config is on `Runner.Listener`'s argv, so job steps (same uid) can read it
from `/proc` — the same as on any self-hosted runner.


## Cost

Cloud Run: 24 `p1-` executions, 1572 execution-seconds total at 2 vCPU / 4 GiB
-> **about USD 0.07** at list price (USD 0.000018 per vCPU-s, 0.000002 per GiB-s;
these prices are from memory, not checked against the billing export). Plus 9
Cloud Build builds (a few minutes each, default pool) and under 1 GB in
Artifact Registry for a few hours. The usage-before snapshot was the
execution list: only `sandbox-bridge-rig-grns7` (12:51) existed before p1.
Well under GBP 5. The billing export was not checked.

## Recreate and clean up

```sh
p1/scripts/up.sh                 # infra + image + job, from an empty project
p1/scripts/launch.sh <tag> [K=V] # one execution (what a dispatcher does per job)
p1/scripts/teardown.sh           # deregister leftovers, delete everything p1-*
```

## Other observations

- The proxy's log lines from concurrent threads sometimes interleave (cosmetic).
- After a broken YAML push, `workflow_dispatch` failed with "Workflow does not
  have 'workflow_dispatch' trigger" — the error message is about the YAML, not
  the trigger. actionlint catches it; run it before pushing.
- `metadata.google.internal` resolves inside the sandbox (to 169.254.169.254)
  although `example.com` does not, so `/etc/hosts` is shared from the host;
  connecting to it fails.

## Teardown (run 2026-11-07 16:25 UTC)

`scripts/teardown.sh` output: cleanup execution `p1-runner-f5ph7` logged
`runners on repo: []` (no runners left at all); deleted jobs `p1-runner` and
`p1-probe`; removed the `p1-job` binding on the secret and both
`logging.logWriter` project bindings; deleted service accounts `p1-build` and
`p1-job`, Artifact Registry repo `p1-runner` (all images), bucket
`gs://p1-<sandbox-project>-build`. The check after it printed `no p1 jobs`, `no p1 repos`,
`no p1 service accounts`, `no p1 buckets`, and the secret policy holds 0 `p1-`
members. `sandbox-bridge-rig`, `rig`, `rig-build` and `rig-job` were left
untouched. Still present, as they cannot be deleted: Cloud Build history (9 builds),
Cloud Logging entries, and the GitHub workflow runs. The files under `p1/` and
`.github/workflows/p1-prompt-test.yml` stay on `main` in the repo.
