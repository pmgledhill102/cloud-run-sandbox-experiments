# Prompt runs

<!-- cspell:ignore mhnjf yamux setuid setgroups rootfs ENETUNREACH cloudaudit -->

One section per cold run. A cold run gets the prompt and nothing else: no
access to the repositories holding the reference implementation, no
conversation history.

## Prompt 5, run 1: 2026-11-07

- **Prompt version:** `05-threat-model.md` as first committed (before the
  revisions below), with the runner proposal's sections 5, 6,
  the Cloud Build shim and 8 pasted in as the design.
- **Agent:** a fresh subagent with web access and no cloud credentials, told
  not to read the reference implementation.
- **Output:** [`runs/05-run1-threat-model.md`](runs/05-run1-threat-model.md),
  about 6,400 words, every requested section present.
- **Cost:** no cloud spend; 28 tool calls, about 6.5 minutes.

**Headline finding, new to us.** Google's sandbox documentation says a
sandbox mounts the host container's root filesystem read-only at `/` by
default, and warns not to keep sensitive files on the host's disk. So the
claim "nothing worth stealing is reachable from the sandbox" holds only while
the host writes no secret to disk. The spike never tested this. It is now a
test in the sandbox-bridge rig (`../experiments/sandbox-bridge/`, the
canary checks in `tests.sh`).

**Other findings worth checking against the real system:**

- Cloud Run's default task retries would reuse a just-in-time config.
  Production sets `maxRetries: 0`? Not verified here.
- "Spent when the runner starts" is not what GitHub documents. A JIT config
  is consumed when a job is assigned, and unused ones stay valid for an
  unstated time.
- The broker is the largest design gap: nothing ties a build's image path or
  cache scope to the job's repository.

**Where the prompt made the agent guess, and what changed:**

| Ambiguity | Prompt change |
| --- | --- |
| No spike results, so "tested" could not be judged | Prompt now asks for evidence or a "claimed, evidence not reviewed" label |
| Design sections 1–4 and 7 missing; timeouts, broker config and registry layout guessed | Paste the whole design, or list what was left out |
| Personal account or organisation; fork PRs | Now a required input |
| "Read access to the project" undefined | Defined as three role sets, modelled separately |
| Sandbox-escape scope | Host container in scope; Google infrastructure out |
| Whether negative controls may use weakened builds | Explicitly allowed, if labelled |
| Reconciler identity, ephemeral-disk mount mode, cache design | Left to the design paste (they are design facts, not prompt facts) |

**Status:** not yet "tested". Run 2 should use the revised prompt with the
whole design and the spike results attached, and be compared with run 1.

## Prompt 1, run 1: 2026-11-07

- **Prompt version:** `01-ephemeral-runner.md` as first committed (before
  the revisions below), inputs `<sandbox-project>`,
  `<throwaway-repo>`, secret `<admin-token-secret>`.
- **Agent:** a fresh subagent, told not to read the reference implementation,
  with gcloud with owner access to the sandbox project and GitHub read and dispatch access. It was
  paused once for about an hour by an account usage limit, then resumed.
- **Output:** code and evidence in `<throwaway-repo>` (private), `p1/`,
  commits `08f4e2a`…`214ac49` on `main`. Notes kept verbatim in
  [`runs/01-run1-notes.md`](runs/01-run1-notes.md).
- **Cost:** about 1,572 execution-seconds of Cloud Run plus 9 small Cloud
  Builds. The agent estimated about $0.07; that's an estimate, not billed.
- **Clean-up, checked independently:** no `p1-` Cloud Run jobs,
  repositories, service accounts, buckets or IAM bindings remain; the rig is
  untouched. The agent reports that no runners remained registered; the
  runner list can't be read from this session, so that rests on its
  evidence (`evidence/teardown-cleanup-*.log`).

**Result: all eight acceptance tests passed, with evidence.** Spot-checked:
A1 (execution `mhnjf`: job succeeded, runner deregistered, `exit(0)`), A4 and
A5 from inside the job with their host-side controls, A6, and A7's table.
Evidence was scanned for secret shapes and with gitleaks: clean.

| Test | Result |
| --- | --- |
| A1–A6, A8 | Pass, with negative controls |
| A7 start-up, n=13 | p50 **38.1 s**, p90 49.5 s. Largest stage is Cloud Run start, p50 30.2 s |
| Kill mid-job | Not fixed (time-boxed), as the prompt allowed: the job failed 10 m 01 s after start, three times out of three |

**It built it differently, and that's worth knowing.** It wrote the whole
thing in Python (proxy, multiplexer, launcher) rather than Go and yamux. It
found the same boundary result as the spike, with one addition: **regular
files on a bind mount do cross**, so a file-polling channel was possible,
though slower. It passed the just-in-time config as the first stdin frame
rather than on the command line, because it noticed the platform logs every
`sandbox do` command line. That exposed a real problem in production,
since fixed.

**Where the prompt made the agent guess, and what changed:**

| Ambiguity | Prompt change |
| --- | --- |
| gcloud has no sandbox flag | Added to a dated "observed, verify" section |
| Sandbox CLI logs full argv | Same, plus a constraint: no secret on any command line |
| Sandbox root can't chown, setuid or setgroups | Same |
| Push to main allowed? (`workflow_dispatch` needs the default branch) | Inputs now say so |
| Token permissions unknown, so dispatcher-side cancel unclear | Inputs state Administration only, no Actions |
| Fail-fast matrix cancelled timing samples | A7 asks for `fail-fast: false` |
| "Check Cloud Run usage" with no billing access | Estimate from execution-seconds, and label it |
| A6 restore when egress is per-execution | Per-execution is now allowed |
| Pauses against the time limit | Pauses don't count |
| Images must use Cloud Build | In the observed section |

**Status:** first cold run passed every acceptance test. Call it "tested
once". A second run on the revised prompt would show whether the dated
facts help, or make the agent skip checks it should still make.

## Prompt B1, run 1: 2026-11-07

- **Agent:** a fresh subagent, the same isolation rules as the runs above,
  sharing the project with B2 under its own `b1-` prefix.
- **Output:** notes kept verbatim in
  [`runs/b1-run1-notes.md`](runs/b1-run1-notes.md). Code stayed in the
  session's scratch space.
- **Cost:** one Cloud Build and three executions of about a minute; under
  £0.10. About 20 minutes.
- **Clean-up, checked independently:** no `b1-` resources remain.

**Result: P1–P5 all passed.** 54 outcomes (check × client × context) were
identical across three executions, and the agent proved its comparison could
report a difference by flipping one outcome in a copy. It matches the rig,
and adds one fact: the default sandbox has only `lo` and an **empty route
table**, so every connection fails at once with `ENETUNREACH`. Nothing times
out. With `--allow-egress` it gets IPv4 only, and the metadata server's HTTP
port still times out.

**Prompt changes:** a dated "observed, verify" section (no gcloud flag, uid 0
despite the docs, own build bucket because of the EU location policy); C1 and
C3 defined precisely; "non-curl client" relaxed for C7 and C8; "execution"
defined; the unavoidable clean-up gap (build history, logs) stated.

## Prompt B2, run 1: 2026-11-07

- **Output:** notes kept verbatim in
  [`runs/b2-run1-notes.md`](runs/b2-run1-notes.md).
- **Cost:** four Cloud Builds (one failed) and seven executions; under £1.
  About 26 minutes.
- **Clean-up, checked independently:** no `b2-` resources remain.

**Result: P1–P4 all passed**, with a host-to-host control for every channel,
passing and failing alike, and results identical in every execution that
ran them. It matches the rig and the spike, and adds:

- **Files on a bind mount are live**: a file written, or rewritten, after the
  sandbox starts is visible, unlike the host's root filesystem.
- **`--import-tar` fails with the same `FetchSpec failed: loading container:
  file does not exist` error** the rig hit with `--rootfs`, when the tar has
  no `./` root entry, and works with one. That's a lead on why `--rootfs`
  failed.
- **The sandbox CLI always exits 1** on a failed command. The real exit code
  is only in the stderr text.
- Abstract Unix sockets and `/dev/shm` don't cross either.

**Prompt changes:** whose loopback (both sides, and the host's own address);
H5's sub-cases named; H7 tested after start too; runs counted per probe
version; `--env` named as a logged channel; the build setup gotchas in the
observed section.

## Prompt B5, run 1: 2026-11-07

- **Target:** a clean copy of the sandbox-bridge rig's tunnel (the proxy and
  bridge production ran before that fix, a neutral launcher, deploy
  scripts), with every test script and finding removed.
- **Output:** the agent's report, kept verbatim in
  [`runs/b5-run1-findings.md`](runs/b5-run1-findings.md). Its harness refused
  a `FINDINGS.md`, so the report came back as its final message.
- **Cost:** two Cloud Builds and three short executions; well under £5.
  About 25 minutes.
- **Clean-up, checked independently:** no `b5-` resources remain.

**Result: P1–P4 passed.** Every attack has evidence, a control and a label.
The headline findings:

| Attack | Label | Finding |
| --- | --- | --- |
| X6 fronting | **bypass**, by design | CONNECT to allowlisted `api.github.com`, then TLS and Host naming `github.com`: **HTTP 200**, from a host not on the allowlist. A direct CONNECT to `github.com` got 403 |
| X7 host files | **exposure** | Root-owned 0600 files in `/root` and `/run` readable from inside; `/tmp` shadowed; the proxy socket visible but refused |
| X8 logged values | **exposure** | `--env` and execution overrides appear in platform logs **and in the System Event audit log** |
| X10 exhaustion | limitation | 300 concurrent streams accepted, and no idle timeout on established tunnels |
| X4, D1 | limitation | Mixed DNS dialled the public address, in either order |
| X1–X3, X5, X9 | defence holds | Direct egress, metadata, URL tricks, rebinding and tunnel-frame abuse all blocked, each with a control that could fire |

**It found a production issue.** X8's audit-log path was confirmed
independently in the sandbox: a `RunJob` entry in
`cloudaudit.googleapis.com/system_event` holds the execution spec with its
overrides applied. Those entries stay in the `_Required` bucket for 400 days
and can't be deleted. The production dispatcher sends each runner's JIT
config as an env override, so that was reported for a fix.

**Prompt changes:** whether the target's own setup counts as project IAM;
EU-only build gotchas in an observed section; "direct" tests must use a tool
that ignores proxy variables; public DNS test names may not resolve, so use
host `/etc/hosts` entries; X8 points at audit logs and the deploy tooling as
well as the platform; extending the launcher for controls is allowed while
the proxy and bridge stay unchanged.

## Prompt B4, run 1: 2026-11-07

- **Output:** notes kept verbatim in
  [`runs/b4-run1-notes.md`](runs/b4-run1-notes.md).
- **Cost:** four Cloud Builds and five executions; well under £1. About 35
  minutes.
- **Clean-up, checked independently:** no `b4-` resources remain.

**Result: T1–T7 all passed**, with every negative control, in execution
`b4-bridge-27gg5`. It built a single Go binary with three roles (proxy, host
end, inside end) over hashicorp/yamux, like the reference, but chose
differently in two places:

- **The command's output goes over the tunnel**, as its own streams, because
  output the sandbox forwarded on stderr was **lost at teardown**: 3 of 20
  result lines went missing in its first version.
- **The proxy refuses a name if any address is non-public**, the stricter
  rule. The reference skips private addresses instead.

**It found how to avoid the 39-second shutdown wait,** and misattributed
its cause. It recorded that `sandbox do` doesn't return until its stdin
reaches end-of-file, 15 s for a 79 ms command. The 2026-11-08 shutdown probe
showed the CLI itself exits within about 32 ms with stdin open, so the wait
was most likely its own bridge's inside end reading stdin. The host must close the
sandbox's stdin itself. A naive shutdown hung until a 45 s watchdog killed
it. With the fix, the host exited 0.013–0.053 s after the command, over 10
runs. That explains the 39-second exit tail, which had been fixed by symptom.

Also measured: 20 concurrent downloads, 92.5 MB, in 2.5 s, every checksum
matching a direct fetch.

**Prompt changes:** the stdin-EOF behaviour and the stderr loss in the
observed section; the host is a Cloud Run job; T5's checksums are against a
direct fetch; T7 may use a separate proxy instance with a longer allowlist;
which methods go with which ports; the build identity's bucket roles.

## Prompt B3, run 1: 2026-11-08

- **Prompt version:** `bridge/b3-stdio-transport.md` as committed before
  this run (revised after it, below). Inputs: the sandbox project, region
  `europe-west1`.
- **Agent:** a fresh subagent told to read only the prompt file and nothing from the reference implementation, with gcloud with owner access to the sandbox project. Every resource
  prefixed `b3-`.
- **Output:** [`runs/b3-run1-notes.md`](runs/b3-run1-notes.md). A Cloud Run
  job whose host process starts `sandbox do -- python3 -u -c ...` with all
  three streams piped; 6 instances, 3 repeats each for S3 and S4.
- **Cost:** about 800 task-seconds at 2 vCPU, roughly £0.03; about 20
  minutes. Clean-up run and checked: no `b3-` jobs or service accounts left,
  with a control search that finds the project's other jobs.

**Result: S1–S8 all answered, P1–P4 met.** Binary-clean (5 MiB, 6 of 6, and
a flipped byte detected); interactive with no batching; one-way 320–540
MB/s and round trip about 200–260 MB/s median, with Python at both ends; 64-byte round
trips p50 about 150 µs, p90 about 175 µs, with 13–29 ms stalls in every run;
the writer blocks with about 64 KiB in flight and nothing is dropped.

**New to us:**

- **Killing the `sandbox` CLI leaves the sandboxed process running** (3 of
  3). `sandbox delete --force <name>` stops it in 69 ms, which needs
  `--sandbox-name` on the original call. A host that times out must delete
  the sandbox, not just kill its child.
- **No half-close from inside.** A sandboxed process that closes stdout but
  keeps running is seen as EOF only when it exits. A tunnel has to send
  close as a frame.
- **One stray write to stdout desynchronises a framed stream for good.** A
  `print` after frame 10 was read as the length `0x5741524e` ("WARN").
- **Cloud Logging lost job stdout**: two lines mid-run once, and all of a
  container's output when it exited straight after printing. A 15 s pause
  before exit fixed it. This matches B4's stderr loss at teardown.

**Conflict with B4, resolved 2026-11-08 in B3's favour** by
`../experiments/shutdown-probe/`: `sandbox do` exited about 32 ms after its
command with stdin held open in all 27 runs (9 cases, including the rig's
flags and unread stdin data). B4's wait most likely came from its own bridge
reading stdin until EOF. The original note follows.

**Conflict with B4, as first recorded.** B3 saw the CLI exit 19–24 ms after the
child exited, **with the host's stdin still open**, on all 6 instances. B4
saw `sandbox do` wait for stdin EOF, 15 s for a 79 ms command, and the full
write-up gives that as the root cause of the 39-second exit tail. The two
rigs differ (B3: `python3 -u -c` with three plain pipes; B4: its bridge
binary as the command), and that may matter, but neither run tested the
other's case. To resolve before publication: run both shapes in one job.

**Where the prompt made the agent guess, and what changed:**

| Ambiguity | Prompt change |
| --- | --- |
| Job, service or worker pool; and the `sandbox` CLI exists only inside a sandbox-enabled container | Says the host is a program inside a Cloud Run job with sandboxes enabled |
| gcloud 570.0.0 rejects `sandboxLauncher` | Observed note: use the Admin API or YAML if gcloud lacks the flag |
| "At least three executions" | Defined as at least three separate instances |
| S7 didn't cover killing the host-side CLI or half-close | S7 now lists both, and orphaned sandboxes |
| No baseline for S3 and S4 | A plain pipe with no sandbox, for comparison |
| How to get results out | Observed note on lost log lines; write results durably or pause before exit |

## Prompt B6, run 1: 2026-11-08

- **Prompt version:** `bridge/b6-benchmark.md` as committed before this run
  (revised after it, below). Inputs: the sandbox project, region
  `europe-west1`, no tunnel supplied, so it built a minimal one.
- **Agent:** a fresh subagent told to read only the prompt file and nothing from the reference implementation, with gcloud with owner access to the sandbox project. Every resource
  prefixed `b6-`.
- **Output:** [`runs/b6-run1-notes.md`](runs/b6-run1-notes.md). A Cloud Run
  job with sandboxes enabled (4 vCPU) against a test server on a private VM
  in the same region, over Direct VPC; its own Go tunnel with 32 KiB frames
  and a CONNECT proxy; R1–R5 plus a 256 KiB-frame variant, 3 executions of 3
  repetitions.
- **Cost:** about £0.45, about 61 minutes. Clean-up run: everything deleted
  except `b6-vpc` and `b6-subnet`, which Cloud Run holds for 1–2 hours after
  a Direct VPC job is deleted. Both are free; the sandbox project's expiry
  removes them regardless.

**Result: P1–P3 met.**

| Path | One stream, 100 MiB | 16 streams | Small request p50 | First request |
| --- | --- | --- | --- | --- |
| R1 host, direct | 551 MiB/s | 540 MiB/s | 0.39 ms | — |
| R2 host, proxy only | 517 MiB/s | 626 MiB/s | 0.46 ms | — |
| **R3 sandbox, tunnel and proxy** | **417 MiB/s** | **358 MiB/s** | **0.88 ms** | **167 ms** |
| R4 sandbox, `--allow-egress` | 386 MiB/s | 540 MiB/s | 0.50 ms | 238 ms |
| R5 files on a bind mount | 372 MiB/s | 432 MiB/s | 2.43 ms | 159 ms |

Medians; the spread is wide, so differences under about 15% are noise.

**Where the cost comes from: CPU.** Raw `sandbox do` stdio moved 1,652–1,858
MiB/s, about 4× the tunnel, so the channel isn't the cap, and neither are
the proxy (R2 ≈ R1) or the frame size. R3 used about 3× the CPU per byte of
R2, most of it in the `sandbox do` process relaying the pipe, and it doesn't
scale with parallel streams because they share one pipe pair. Start-up is
almost all the sandbox itself, about 150 ms.

**It also tried, and was refused, a public test server.** Its first deploy
would have made the server reachable without authentication
(`--allow-unauthenticated`) and included a skip-TLS-check switch. The
session's permission checks refused it; the agent didn't retry, removed the
switch, and used a private VM instead.

**Where the prompt made the agent guess, and what changed:**

| Ambiguity | Prompt change |
| --- | --- |
| Job or service; what "the proxy" is | The host is a Cloud Run job; the proxy is an allowlisting CONNECT proxy, with TLS end to end |
| A home-made tunnel makes R3 a measure of that code | Prefers a supplied tunnel; if built, says so and decomposes R3 |
| How the test server is reached | A private server reached over Direct VPC; never a public unauthenticated one |
| "Three executions"; M2 bytes per stream; M3 connection reuse; M4 without a sandbox; M5 scope | Each defined |
| No decomposition | Adds raw `sandbox do` stdio and a host-loopback ceiling |
| gcloud lacks the sandbox flag; Direct VPC holds the subnet after deletion | Observed notes |

## Prompt B7, run 1: 2026-11-08

- **Prompt version:** `bridge/b7-agent-gateway.md` as first committed
  (revised after this run, below). Inputs: the sandbox project, region
  `europe-west1`, no tunnel supplied, **Gemini on Vertex AI only**, capped at
  30 model requests for the whole run. No Anthropic key existed.
- **Agent:** a fresh subagent told to read only the prompt file and nothing from the reference implementation, with gcloud with owner access to the sandbox project. Every resource
  prefixed `b7-`; one project grant, `roles/aiplatform.user` for its job
  identity, removed in clean-up.
- **Output:** [`runs/b7-run1-notes.md`](runs/b7-run1-notes.md). Its own
  Python gateway, allowlisting proxy and length-prefixed stdio tunnel
  carrying two named services, Gemini CLI 0.67.0, and a task repo with a
  stand-in compromised dependency.
- **Cost:** under £1, about 1 hour 20 minutes. Clean-up run and checked: no
  `b7-` job, repository, service account or bucket, and no `b7-` binding in
  the project's IAM policy, with a control search that finds the POC's own
  binding.

**Result: A1–A9 all passed.** The agent fixed the bug inside the sandbox in
5 model calls, all through the gateway. The planted dependency found only
the canary, couldn't reach the metadata server or connect out, and every
non-model gateway request got 403 in all three contexts. A call with a
client-supplied key still used the gateway's credential, and the cap
answered 429 without an upstream call. The logged `sandbox` command lines
carried no secret.

**It independently matched the POC** on the points that matter: no
credential in the sandbox, the metadata server unreachable even with
`--allow-egress`, and Gemini CLI's gateway mode needing a workaround (it
used `security.auth.useExternal`, where the POC selected `gemini-api-key`).

**New to us:**

- **Only Gemini 3.x on the global endpoint generated.** Every 2.x model
  returned 404 in every region it tried, though their model cards exist.
- **Gemini CLI promotes the model id** (`gemini-3.5-flash` to
  `gemini-3.8-flash`), so a gateway that allowlists model ids must allow
  the promoted one or pin upstream.
- **`HTTPS_PROXY` sent the model call to the package proxy.** With an
  `http://` base URL the CLI wraps the call in the proxy agent, and the
  allowlist refused it. The POC avoided this with `NO_PROXY=127.0.0.1`.
- **The CLI's shell tool needs `bash`.** Without it the agent spent 8–9
  turns and failed; with it, 5 turns and 28 s.

**The request cap overran.** By the agent's count, about 33 Gemini requests:
about 25 through the gateway across six debugging executions, plus 8 direct
probes to find a working model. The logs saved for runs 4–6 show 20. The
gateway enforced its cap within each execution, but a fresh container can't
see earlier runs, and the probes bypassed it. With the POC's 7, the day's
Gemini total was about 40, inside the 50 the owner set.

**Where the prompt made the agent guess, and what changed:**

| Ambiguity | Prompt change |
| --- | --- |
| Which model and endpoint the project can call | Inputs now name a model and endpoint verified beforehand, or allow a small, counted probe budget |
| Whether the cap covers set-up probes and every execution | Defined: every model request of any kind, across all executions, kept in a running tally outside the container |
| `HTTPS_PROXY` routing the model call through the package proxy | Observed note: exempt the gateway with `NO_PROXY` |
| The CLI's shell tool needs `bash`; model-id promotion | Observed notes |
| The CLI's `gateway` auth type | Both known workarounds listed |
