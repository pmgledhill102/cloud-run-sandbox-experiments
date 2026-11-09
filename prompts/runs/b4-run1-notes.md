# b4: allowlisted egress for a no-network Cloud Run sandbox, over stdin/stdout

Date: 2026-11-07 (UTC). Project `<sandbox-project>`, region `europe-west1`.
Everything created is named `b4-*`.

## Docs read (2026-11-07)

- Code execution in Cloud Run (sandboxes): https://docs.cloud.google.com/run/docs/code-execution
- `sandbox` CLI reference: https://docs.cloud.google.com/run/docs/reference/sandbox-cli
- Configure sandboxes for jobs: https://docs.cloud.google.com/run/docs/configuring/jobs/sandboxes
- Configure sandboxes for services: https://docs.cloud.google.com/run/docs/configuring/services/sandboxes

Facts used from them: the binary is `/usr/local/gcp/bin/sandbox`; egress is
blocked by default and `--allow-egress` opens it; the sandbox sees the host
rootfs read-only and has its own in-memory `/tmp`; env is not inherited;
`sandbox do --stdin` (default true) forwards caller stdin; enable with
`sandboxLauncher: true` on the container (launch stage BETA).

Doc vs tool mismatch: the docs say `gcloud beta run jobs create --sandbox-launcher`
and `gcloud run jobs replace` with `sandboxLauncher: true`. With gcloud
570.0.0 / beta 2026.05.22, neither works: there is no such flag on `run jobs`,
and both `run jobs replace` and `beta run jobs replace` fail with
`Failed to parse value(s) in protobuf [Job]: ...containers[0].sandboxLauncher`.
The API accepts the same document, so `deploy_job.py` sends `job.yaml` as JSON
to the v1 REST endpoint (reading the token inside the script, never printing it).

## Versions (pinned)

| Thing | Version |
| --- | --- |
| Go (builder) | `golang:1.24.7-bookworm` @ `sha256:b8bae5bd9ba9b1f89b635c91c24cc75cea335a16fb5076310f38566fc674b1ec` |
| Runtime base | `debian:12.12-slim` @ `sha256:d5d3f9c23164ea16f31852f95bd5959aad1c5e854332fe00f7b3a20fcc9f635c` |
| Mux library | `github.com/hashicorp/yamux v0.1.2` (go.sum pinned) |
| curl / python3 / ca-certificates | `7.88.1-10+deb12u15` / `3.11.2-1+b1` / `20250419~deb12u2` |
| `sandbox` binary | no `--version` flag; 57,516,864 bytes, mtime Nov 6 16:49, sha256 prefix `f1f88a5625618d89` |
| Sandbox kernel (reported) | `6.9.12` |
| gcloud (workstation) | 570.0.0, beta 2026.05.22 |
| Final image | `europe-west1-docker.pkg.dev/<sandbox-project>/b4-repo/b4bridge:v3` @ `sha256:2e5fa67bf39622dcc161f18d27f7caa6b914e075eaaa18556546ff2464b25e96` |

## Probe: is the boundary binary-clean? (done before building the tunnel)

Execution `b4-bridge-8brfx` (repeated in every later run):

- 9,437,196 bytes (every byte value x4096, 8 MiB urandom, `\r\n\n\r\0\x1a\x03\x04\x1b[0m`)
  through `sandbox do -- /bin/cat`: same sha256 out as in, 0 bytes on stderr. **Clean.**
- `printf '\0\r\n\377'; echo ERRLINE >&2`: stdout hex `000d0aff`, stderr `ERRLINE`.
  No tty, no CRLF translation, streams kept separate.
- The `[start] cwd=... "<full command line>"` / `[end] exit_code=...` lines that
  appear in the job log come from the platform, not from the sandbox's stdout
  (stdout stayed byte-identical). This is the "the platform logs command lines" fact.
- **`sandbox do` does not return until its stdin reaches EOF**, even after the
  command inside has exited: `(sleep 15) | sandbox do -- /bin/true` took 15.005 s
  while the platform logged `[end] elapsed=79ms`. `sandbox do --stdin=false -- true`: 0.14 s.
  This single fact drives the shutdown design.
- Inside the sandbox: uid 0, empty routing table, `/sys/class/net` absent,
  resolv.conf points at 169.254.169.254 (unreachable).

## Design

```text
Cloud Run job container (trusted)                       sandbox (no network)
 b4bridge proxy :18080 (127.0.0.1 only)                  b4bridge inside
   allowlist, port, IP-literal, resolve+check, dial IP     listens 127.0.0.1:3128 -> stream "proxy"
 b4bridge host --service proxy=127.0.0.1:18080             fd1 = tunnel only; fd1 of itself -> stderr
   spawns: sandbox do ... -- b4bridge inside ...           wrapped cmd: HTTPS_PROXY=http://127.0.0.1:3128
   owns child's stdin/stdout = yamux session  <=========>  yamux client over its stdin/stdout
```

- **One binary, three roles** (`bridge/`): `proxy`, `host`, `inside`.
- **Multiplexing: hashicorp/yamux v0.1.2.** Mature (Consul/Nomad/Vault), pure
  Go, per-stream flow control (stops one fast download starving the rest),
  half-close via FIN, no dependency beyond the stdlib. Stream window raised to
  1 MiB; keepalive 10 s.
- **Stream protocol**: opener (always inside) sends `len,name`; host replies one
  byte (0 ok, 1 unknown service, 2 dial failed). The host dials only addresses
  in its `--service name=addr` map. Reserved names `\x00stdout`, `\x00stderr`,
  `\x00exit` carry the wrapped command's output and exit report.
- **Inside stdout discipline**: `inside` dups fd 1 for the tunnel, then points
  fd 1 at stderr and fd 0 at /dev/null, so no stray write from the bridge or the
  command can reach the tunnel. The wrapped command's stdout/stderr go to their
  own yamux streams (the host writes them to its stdout/stderr). Reason: in v1 they
  went to the sandbox's stderr and **lines were lost at teardown** (T5 v1: 17 of
  20 result lines, one truncated mid-line). Over the tunnel they arrive complete
  and ordered before the exit report.
- **Shutdown**: when the wrapped command exits, `inside` kills its process
  group, FINs its output streams, sends `\x00exit` (code + timestamp), waits
  <=500 ms for an ack, closes the session and exits. The host, on the exit
  message (or tunnel close, or child exit), drains output (<=500 ms), closes the
  session **and the sandbox CLI's stdin** itself, then waits <=1.5 s for
  `sandbox do` before killing it and `sandbox delete --force <name>`. A
  `--watchdog` bounds the whole run. `cmd.WaitDelay=250ms` stops a background
  child that inherited stdout from holding the exit open.
- **Proxy**: `CONNECT` and absolute-form `http://` only. Order: not an IP literal
  -> port 80/443 -> exact allowlist match -> resolve (5 s) -> **refuse if any**
  address is unspecified/0.0.0.0/8, loopback, private (incl. ULA), link-local,
  multicast, CGNAT 100.64/10, 240/4, NAT64 -> dial the checked `IP:port` (v4
  first) — never the name again. Plain HTTP is forwarded in origin form with
  `Connection: close`, one request per connection, `Proxy-*` headers stripped.
  One JSON line per decision to stdout (`host, port, decision, reason, addr,
  method, client, severity`), which Cloud Logging parses as `jsonPayload`.
- **Secrets**: the design carries none. No `--env` value or argument on any
  `sandbox` command is secret (`PATH`, `HOME` only), and the host bridge gives
  the sandbox CLI a scrubbed env (`PATH` only). Nothing is written outside
  `/tmp` on the host. The job SA `b4-job` has no project roles.
- `--allow-egress` appears in exactly one place: T3's control (`b4-t3c`).

## Files

| Path | What |
| --- | --- |
| `bridge/main.go`, `bridge/bridge.go`, `bridge/proxy.go`, `go.mod`, `go.sum` | proxy + both bridge ends |
| `image/Dockerfile`, `image/cloudbuild.yaml`, `image/.gcloudignore` | image, built by Cloud Build |
| `tests/entrypoint.sh`, `tests/t5.sh` | the job: probe + T1–T7 + X1, emits `B4RESULT` lines |
| `job.yaml`, `deploy_job.py` | job definition, and its deployer (gcloud cannot parse it) |
| `recreate.sh`, `run_exec.sh`, `cleanup.sh` | recreate from nothing / run+collect logs / tear down |
| `out/exec-*.log` | collected job logs used as evidence below |

Recreate: `./recreate.sh v3 && ./run_exec.sh all out/run.log`.

## Acceptance tests — final run: execution `b4-bridge-27gg5`, image v3 (log `out/exec-all3.log`)

| # | Result | Evidence |
| --- | --- | --- |
| PROBE | PASS | 9,437,196 bytes round-trip with identical sha256; see above |
| T1 | PASS | inside: `curl https://api.github.com/zen` CODE=200,CONNECT=200; python `urllib` PYCODE=200; `https://speed.cloudflare.com/__down` 200; plain `http://speed.cloudflare.com/...` 200 (absolute-form). Proxy: `allow allowlisted addr=140.82.121.5` etc. |
| T2 | PASS | `https://example.com` CODE=000,CONNECT=403 (`curl: (56) CONNECT tunnel failed, response 403`); python `Tunnel connection failed: 403 Forbidden`; plain http 403. Proxy: `deny host-not-allowlisted`. **Control** (proxy with example.com added, :18081): curl 200, python 200, `allow addr=104.20.23.154` |
| T3 | PASS | inside, `--noproxy '*'`: api.github.com `curl: (6) Could not resolve host`; 1.1.1.1:443 `(7) Couldn't connect`; host proxy port 127.0.0.1:18080 directly `(7) Couldn't connect` (host loopback is not shared); python socket to 140.82.121.6:443 `Errno 101 Network is unreachable`. **Control** `sandbox do --allow-egress`: DCODE=200 |
| T4 | PASS | inside direct: 169.254.169.254 and metadata.google.internal `(7) Couldn't connect`. Through the proxy (HTTP and CONNECT): 403 — `ip-literal` for 169.254.169.254, `host-not-allowlisted` for the name; and with metadata.google.internal deliberately allowlisted (:18082) still 403 `resolves-to-link-local:169.254.169.254`. **Control** from the host container: direct 200 |
| T5 | PASS | 20 parallel `curl`s of `speed.cloudflare.com/__down?bytes=2.25M..7M` (92.5 MB) in 2.53 s wall (8.7 s in an earlier run); 20/20 sizes and sha256 equal to a host-direct fetch of the same URL (payload is not zeros: sha of 2,250,000 zeros differs) |
| T6 | PASS | wrapped `sh -c 'curl <500MB> & sleep 3; exit 7'` (an open tunnel stream at exit): host event `cmd_exit_to_host_exit_s=0.013`, `stop=exit-message`, `stop_to_child_exit_s=0.013`, host rc=7 (exit code propagated), outer wall 3.146 s for a 3 s command. Across all 10 non-control bridged runs in the final execution: 0.013–0.053 s |
| T6 control | hangs | `--naive-shutdown` on both ends (host waits for the sandbox CLI; inside waits for the tunnel): no progress until the 45 s watchdog killed it, `cmd_exit_to_host_exit_s=41.94`, `killed:true`. Without a watchdog it waits forever, because `sandbox do` waits for stdin EOF (probe). An intermediate v1 (host only reacted to the tunnel closing, which never happens since `sandbox do` keeps its stdout open while stdin is open) exited after **9.2–9.8 s**, rescued only by the yamux keepalive (exec `b4-bridge-gz5qx`) |
| T7 | PASS | proxy :18082 (allowlist also holds localtest.me, 127.0.0.1.nip.io): CONNECT 1.1.1.1 / 140.82.121.6 / [::1] and GET http://1.1.1.1 → `ip-literal`; api.github.com:22 and :8080 → `port-not-allowed`; 127.0.0.1.nip.io → `resolves-to-loopback:127.0.0.1`; localtest.me → `resolves-to-loopback:::1`. 11 denies, 0 allows in that proxy's log |
| X1 (extra) | PASS | inside listener mapped to service `metadata` (not configured on the host): host logs `stream-refused unknown-service metadata`, client gets a reset |

Timings: `sandbox do -- true` 0.14–0.19 s; bridged runs total 0.17–0.75 s for
short commands; full suite execution ~80 s plus the 45 s control.

## Spend and time

4 Cloud Build builds (~1.5 min each, default pool) and 5 job executions
(2 vCPU / 4 GiB, ~1–3 min each). Estimated well under £0.50. About 35 minutes.

## Clean-up

See the bottom of this file for `cleanup.sh` output.

### `cleanup.sh` output (run 2026-11-07 ~22:00 UTC, full text in `out/cleanup.log`)

Deleted: job `b4-bridge`; Artifact Registry repo `b4-repo` (images v1–v3);
bucket `gs://b4-<sandbox-project>` (build sources and build logs); service
accounts `b4-build`, `b4-job` (their resource-level IAM bindings went with the
resources). Verification listed no remaining `b4-*` jobs, repos, buckets or SAs.

Left behind (cannot be deleted, or not worth it): Cloud Build history entries
for the 4 builds; the job's Cloud Logging entries (default retention). No
project-level IAM was changed. Nothing not named `b4-*` was modified.
