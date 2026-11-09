# sandbox-agent POC: coding agents in a no-egress Cloud Run sandbox

<!-- cspell:ignore gVisor yamux pypi pythonhosted npmjs vpmj xh9pn sljj lx9h2 dkkj c89qx 9d6xs behavior -->

Evidence for the article's second workload, coding agents, alongside CI.

Can a coding agent work inside a Cloud Run sandbox with **no network**,
reaching only allowlisted hosts, and reaching its model **without ever
holding a credential**? Tested with Claude Code and Gemini CLI on a small
task, with a stand-in compromised dependency trying to get out.

**Experimental.** A test harness, not a product. It ran in a broker-created
sandbox project with a spend cap and expiry.

## The design

```text
sandbox (no network, uid 0, gVisor)          host container (trusted)
  agent: claude -p / gemini -p                 egress-proxy   allowlist: pypi, npm
    HTTPS_PROXY     127.0.0.1:3128 ─┐           model-gateway
    model base URL  127.0.0.1:8080 ─┤ stdin/      claude: + key from Secret Manager ──▶ api.anthropic.com
                    127.0.0.1:8081 ─┘ stdout ──▶  gemini: + host's own token     ──▶ Vertex AI
                                     (stdio-bridge, unchanged from the sandbox-bridge rig)
```

- **The bridge and proxy are the [sandbox-bridge](../sandbox-bridge/) rig's,
  unchanged.** The bridge already carries several named host services, so
  the model gateway is configuration, not new transport code.
- **`model-gateway`** (`gateway/`, Go standard library only) listens on one
  Unix socket per backend. It accepts only `POST` to model endpoints, drops
  any credential the client sent, adds its own, and forwards over TLS. It
  streams responses as they arrive, caps requests per backend, and logs
  method, path, status, size and latency, never bodies.
  - **Claude:** the Anthropic key is read from Secret Manager into memory at
    start with the host's identity. It's never on disk or a command line.
    Claude Code is pointed at it with `ANTHROPIC_BASE_URL` and a placeholder
    key.
  - **Gemini:** Gemini CLI speaks the Gemini API to the gateway
    (`GOOGLE_GEMINI_BASE_URL`, placeholder key), and the gateway translates
    to Vertex AI with the host's own service-account token. **No Gemini key
    exists anywhere.** Capped at 40 requests.
- **The model APIs aren't on the egress allowlist.** The only way to a
  model is the gateway.

## The test: a compromised dependency

The task repo (`task/`) has one bug for the agent to fix. Its package imports
`calc/_metrics.py`, a stand-in for a compromised dependency. Whenever the
agent runs the tests, it runs too, with the agent's privileges, and tries to:

- find a credential in the environment or any readable file (Anthropic key
  and Google token prefixes);
- reach the metadata server, directly, by name and through the proxy;
- connect out directly, by name and by IP, and through the proxy to a host
  that isn't allowlisted;
- use the gateway for anything but model calls: listing models, another API,
  path traversal, another method.

It reports what it reached, with counts and paths only. It never makes a
billable model call. The launcher also plants a **fake key on the host's
disk** before the sandbox starts (`/etc/agent-canary`), so the scan has a
case it must find.

Attack mode runs the probe with no agent in three contexts: the sandbox, the
sandbox with `--allow-egress` (negative control), and the host container
(control). Each blocked result in the sandbox is evidence only because a
control shows the same action can succeed.

## Results, 2026-11-08

Claude Code 2.1.319, Gemini CLI 0.67.0 with `gemini-3.8-flash`, sandbox
kernel `4.19.0-gvisor`, image `sandbox-agent:v4` unless stated. Raw logs are
in `results/`, one file per execution.

### The agents did the work

| Agent | Execution | Model requests | Time in the sandbox | Result |
| --- | --- | --- | --- | --- |
| Claude Code | `vpmj6` (v2) | 3, all 200 | 17 s | Fixed the bug, tests pass |
| Claude Code | `9d6xs` | 3, all 200 | about 19 s | Fixed the bug, tests pass |
| Gemini CLI | `lx9h2` | 7, all 200 | 53 s | Fixed the bug, tests pass |

Claude Code ran as root with `IS_SANDBOX=1`, which its
`--dangerously-skip-permissions` check requires under uid 0. It noticed the
planted module unprompted: "Importing `calc/_metrics.py` also prints an
`ATTACK {...}` diagnostics line".

### What the compromised dependency could reach

From attack mode, execution `xh9pn`. The probe ran 3 times inside Claude
Code's run (`9d6xs`) and 2 times inside Gemini CLI's (`lx9h2`), and every
report matched this sandbox column for the backend that was running.

| Check | Sandbox | `--allow-egress` | Host |
| --- | --- | --- | --- |
| Secret in environment | none | none | none |
| Secret on disk | **only the planted canary** | only the canary | only the canary |
| Metadata server, direct or by name | **blocked** | blocked (timeout) | reached, 200 |
| Metadata server through the proxy | blocked, 403 | blocked, 403 | blocked, 403 |
| Direct egress, by name or IP | **blocked** | reached | reached |
| Proxy, host not on the allowlist | blocked, 403 | blocked, 403 | blocked, 403 |
| Proxy, allowlisted host (control) | reached | reached | reached |
| Gateway: list models, other API, traversal, other method | **403** | 403 | 403 |

- **The agent never holds a model credential.** The only secret-shaped
  value anywhere the probe could look is the one the launcher planted. That
  finding shows the scan works, and that the real key never touched disk.
- **The same canary result reconfirms the rig's earlier finding:** whatever
  the host writes to disk before a sandbox starts is readable inside it. The
  gateway keeps its key in memory for that reason.
- **The gateway is the only route to a model, and only for model calls.**

## Findings along the way

- **The agent's own logs hide what its tools do.** When the agent runs the
  tests, it captures their output as tool output, so the probe's report went
  to the agent rather than the job log. The probe now also writes a file
  that the wrapper prints at the end. An agent sandbox needs its own
  evidence channel, independent of the agent.
- **Gemini CLI 0.67.0 has a half-shipped gateway mode.** Its core supports a
  `gateway` auth type selected by `GOOGLE_GEMINI_BASE_URL`, but the CLI's
  auth validation rejects it ("Invalid auth method selected", exit 41)
  before any request. Selecting plain Gemini API auth with a placeholder key
  works, and the core still honours the base URL. `--skip-trust` is the
  CLI's documented route for folder trust in headless runs.
- **The command line stays clean.** The platform logs every `sandbox do`
  command line. Here it carries only `PATH` and a context name. Placeholders
  are set inside the sandbox, and the real credentials never leave the host.

## Limits

- **A model call is still an exfiltration channel.** Anything the agent can
  read, it can put in a prompt, and the gateway forwards prompts by design.
  The sandbox and gateway keep the *credential* and the *host's identity*
  away from the agent. They don't stop it sending what it can see to the
  model provider.
- **Allowlisted registries are a channel too**, as in the sandbox-bridge
  rig: an allowlist narrows where data can go, it doesn't stop it going.
- **One small task, one model each, few runs.** The result is that the
  mechanism works with two real agents, not a measure of either.
- **Preview.** Cloud Run sandboxes are pre-GA; every result is dated.

## Reproduce

Needs a sandbox project with Cloud Run, Cloud Build, Artifact Registry
(`rig` repository), Secret Manager and Vertex AI enabled; a job identity
`agent-job` with `roles/aiplatform.user`, `roles/logging.logWriter`, and
`secretAccessor` on `sandbox-agent-anthropic-key`; and the build identity
`rig-build` from the sandbox-bridge rig.

```sh
# From experiments/ (this directory's parent)
gcloud builds submit --project "$PROJECT" --region europe-west1 \
  --config sandbox-agent/cloudbuild.yaml \
  --substitutions _IMAGE=europe-west1-docker.pkg.dev/$PROJECT/rig/sandbox-agent:v4 \
  --service-account "projects/$PROJECT/serviceAccounts/rig-build@$PROJECT.iam.gserviceaccount.com" \
  --default-buckets-behavior=regional-user-owned-bucket .
cd sandbox-agent
PROJECT=<id> IMAGE=<image> ./run.sh attack      # or claude, gemini
PROJECT=<id> python3 results.py <execution>     # save and summarise
cd gateway && go test ./...                     # gateway rules, offline
```

`results.py` refuses to save a log that contains a key- or token-shaped
value. The gateway tests were checked by breaking the gateway on purpose: no
flush in the response wrapper, and the client's `Authorization` header passed
through. Both made a test fail.

## Runs

| Execution | Image | Mode | Notes |
| --- | --- | --- | --- |
| `5dkkj` | v1 | attack | Only the Gemini backend up. Secret-file hits were the probe matching its own source; fixed in v2 |
| `c89qx` | v2 | attack | Both backends; clean |
| `vpmj6` | v2 | claude | Worked first time; the probe's report was captured by the agent |
| `xh9pn` | v3 | attack | Host canary added; found, nothing else |
| `2sljj` | v3 | gemini | CLI auth validation, exit 41, no model request |
| `lx9h2` | v4 | gemini | Worked |
| `9d6xs` | v4 | claude | Re-run with the report file and canary; worked |
