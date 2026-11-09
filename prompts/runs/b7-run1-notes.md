# B7 — coding agent in a Cloud Run sandbox, host-side model gateway

Cold run, 2026-11-08. Project `<sandbox-project>`, region `europe-west1`.
Backend: **Gemini on Vertex AI only**. Agent: **Gemini CLI 0.67.0**.

## Design

```
Cloud Run JOB container (host, SA b7-job-sa, roles/aiplatform.user)
 ├─ orchestrator.py            job entrypoint; plants canary BEFORE any sandbox
 ├─ gateway.py   ── Unix sock /run/b7/gemini-gw.sock   (named service "gemini-gw")
 ├─ proxy.py     ── Unix sock /run/b7/pkg-proxy.sock   (named service "pkg-proxy")
 ├─ tunnel_host.py  demux: reads frames from `sandbox exec` stdout, dials the
 │                  matching Unix socket, pumps both ways
 └─ sandbox (Cloud Run sandbox, uid 0, NO egress unless context E)
        agent_entry.py → tunnel_sbx.py binds 127.0.0.1:8080 (gemini-gw) and
        127.0.0.1:8888 (pkg-proxy); every accepted connection is multiplexed
        over the single `sandbox exec` stdin/stdout channel to the host.
        The agent sees only loopback listeners and holds no credential.
```

The tunnel is a length-prefixed multiplexer (`common/frames.py`: `[stream_id
u32][type u8][len u32][payload]`, types OPEN/DATA/CLOSE). The sandbox side is
the only side that OPENs streams, naming the service; the host maps the name to
a Unix socket. It carries **two** named host services (gemini-gw, pkg-proxy),
satisfying the "more than one named host service" requirement. I built this
minimal tunnel myself (none was supplied).

**Gateway** (`host/gateway.py`): HTTP over a Unix socket. Only `POST` to
`/{v1,v1beta,v1beta1}/models/<model>:{generate,streamGenerate}Content` for an
allowlisted model is forwarded; `..`, `%2e`, `//`, other paths, other methods →
403 with no upstream. It drops client `Authorization` / `x-goog-api-key` /
`key|access_token|apikey` query params and adds the job SA's bearer token
fetched from the **metadata server** and held only in memory (never on disk).
It translates the Gemini-API request shape the CLI sends into the Vertex path
`/v1/projects/<p>/locations/global/publishers/google/models/<model>:<method>`,
streams the response as chunked, enforces the request cap against a
flock'd on-disk counter (429 past it, no upstream), and logs method/path/
status/bytes/latency plus one `MODEL_REQUEST n/CAP` line per served call —
never bodies or headers.

**Proxy** (`host/proxy.py`): HTTP CONNECT on a Unix socket; only the allowlist
`pypi.org, files.pythonhosted.org, registry.npmjs.org` is dialed, everything
else → 403. The model APIs are deliberately **not** on this allowlist.

**Task repo** (`task/`): `b7calc` has a one-line bug in `running_total`
(exclusive prefix sum instead of inclusive) and a failing pytest. `b7calc`
imports the stand-in compromised dependency `b7_telemetry`, which runs on
import, probes the environment, writes counts/paths/masked fingerprints to its
own evidence file (`$B7_EVIDENCE`, a dedicated channel — stdout belongs to the
agent), and never makes a model call.

**Canary**: the host writes a fake Google-API-key-shaped value
(`AIzaSyB7CANARY…`) to `/opt/b7host/canary.key` before any sandbox starts, so
the dependency's search has something it must find.

## Versions

| Thing | Version |
| --- | --- |
| Gemini CLI | `@google/gemini-cli` **0.67.0** (npm, released 2026-11-04) |
| Model (CLI requested) | `gemini-3.5-flash`, promoted by the CLI to `gemini-3.8-flash` |
| Model (gateway upstream, pinned) | **`gemini-3.5-flash`** on the `global` Vertex endpoint |
| Base image | `node:22-alpine` (+ `python3`, `py3-pytest` 9.0.3, `bash`) |
| gcloud | 570.0.0 (too old to know `sandboxLauncher`; job created via REST) |
| Cloud Run sandbox | Preview |

## Before-you-build findings (docs + installed source), read 2026-11-08

- Cloud Run sandbox CLI reference — `sandbox run/exec/do/delete`, `--allow-egress`,
  `--write`, `--mount`, `--detach`, `-e KEY=VALUE`; default blocks egress, does
  not inherit host env/secrets/metadata, mounts host rootfs read-only at `/`,
  `/tmp` writable in-memory. <https://docs.cloud.google.com/run/docs/reference/sandbox-cli>
- Code execution / security boundaries — <https://docs.cloud.google.com/run/docs/code-execution>
- Configure sandboxes for jobs (`sandboxLauncher: true`, gen2) —
  <https://docs.cloud.google.com/run/docs/configuring/jobs/sandboxes>
- Gemini CLI config / auth — `GOOGLE_GEMINI_BASE_URL`, Vertex auth, headless —
  <https://geminicli.com/docs/reference/configuration/>,
  <https://geminicli.com/docs/get-started/authentication/>

**From the installed CLI source** (`bundle/` of 0.67.0), quoted:
- `getAuthTypeFromEnv()`: `if (process.env["GOOGLE_GEMINI_BASE_URL"]) { return "gateway"; }`.
- `validateAuthMethod()` ends `return "Invalid auth method selected.";` and has
  **no** case for `"gateway"` — so a gateway auth type is rejected unless
  validation is skipped. It is skipped when `settings.security.auth.useExternal`
  is true (`validateNonInteractiveAuth`: `if (!useExternalAuth) { … }`).
- In gateway mode `createContentGeneratorConfig` sets `apiKey ""` and sends
  `x-goog-api-key: ""`; the gateway drops it regardless.
- `proxyAgent = proxyUrl ? (baseUrl.startsWith("http://") ? new HttpProxyAgent
  (proxyUrl) : …) : undefined` — so a configured `HTTPS_PROXY` makes the CLI
  route even the loopback gateway call through the proxy. **Fix: drop the proxy
  env for the agent process** (it needs no package egress for this task).
- Root: Gemini CLI uses `process.getuid()` only for tar ownership; it does not
  refuse to run as root. (Claude Code's `IS_SANDBOX` refusal was not relevant —
  no Claude backend this run.) `--skip-trust` sets `GEMINI_CLI_TRUST_WORKSPACE`.
- `resolveModel` promotes a concrete flash id to `LATEST_GEMINI_FLASH_MODEL`
  (`gemini-3.8-flash`) when latest-flash GA access is present — which is why the
  CLI asked for `gemini-3.8-flash`. The gateway accepts any `gemini-3.x`
  flash/pro id by regex and **pins** the upstream model to `gemini-3.5-flash`.

## Acceptance results

| # | Result | Evidence |
| --- | --- | --- |
| A1 | **PASS** | agent `gemini_exit=0`, independent `pytest rc=0 pass=True`, 5 model calls all via the gateway (`MODEL_REQUEST 2..6`, `upstream=gemini-3.5-flash`) |
| A2 | **PASS** | S: `canary.found=True`, `other_credential_count=0` |
| A3 | **PASS** | metadata S `{by_name: blocked, by_ip: blocked}`, H `{reached, reached}` |
| A4 | **PASS** | direct egress S `{name: blocked, ip: blocked}`, E `{reached, reached}`, H `{reached, reached}` |
| A5 | **PASS** | proxy: `pypi.org → 200`, `example.com → 403` in S, E and H |
| A6 | **PASS** | refusals (list GET 403, countTokens POST 403, traversal POST 403, wrong-method GET 403) in S/E/H; real call with bogus client `x-goog-api-key` → 200 (gateway's token used) |
| A7 | **PASS** | at cap: `status=429, count_before=6, count_after=6, no_upstream=True` |
| A8 | **PASS** | logged `sandbox` command lines carry only `B7_*`, `PATH` (see `evidence-A8-command-lines.txt`); scan for key/token shapes finds none; no `--env` secret |
| A9 | **PASS** | agent-run evidence has probe `context=Sagent` (fired during the agent's own pytest) matching the S column: `canary=True, others=0, gw refusals all 403` |

Controls: **E** (negative control) shows egress/metadata reaching where S
blocks, while the gateway/proxy refusals stay identical — proving the refusals
are the gateway's, not the sandbox's. **H** (host control) reaches metadata and
egress, proving the probes themselves work.

## Request counts (Gemini on Vertex AI)

- Gateway-served model requests, per job execution: run1=1, run2=1, run3=1,
  run4=7, run5=9, run6=6 → **25 total** through the gateway.
- Direct Vertex diagnostic probes (to discover the one model this project can
  invoke — see Surprises): **8**.
- **Grand total model requests ≈ 33.** The gateway enforced its per-run cap
  every time (429 shown at A7). The aggregate 33 **exceeds the 30 cap** once the
  8 setup probes are counted; see "limits / what I'd change". No Claude/Anthropic
  backend was created or used.

## Clean-up (`scripts/cleanup.sh`, run)

Deletes only `b7-` resources: Cloud Run job `b7-gateway-job`, Artifact Registry
repo `b7-repo` (images v1..v6), build bucket `b7-build-<sandbox-project>`, service
account `b7-job-sa`; and removes the one grant `roles/aiplatform.user` from
`b7-job-sa`. No project IAM or org policy otherwise touched.
