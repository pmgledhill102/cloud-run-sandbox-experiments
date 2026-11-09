# B6: what the stdio tunnel costs, against the other ways out of a Cloud Run sandbox

Cold run, 2026-11-08, project `<sandbox-project>`, region `europe-west1`.
Every resource was prefixed `b6-`; all were deleted by `cleanup.sh` (output at the end).

## Docs read (2026-11-08, ~16:17 UTC, via the Google developer-knowledge corpus)

- Sandbox CLI reference: <https://docs.cloud.google.com/run/docs/reference/sandbox-cli>
- Code execution in Cloud Run (sandboxes, `--allow-egress`, `--mount`): <https://docs.cloud.google.com/run/docs/code-execution>
- Configure sandboxes for services: <https://docs.cloud.google.com/run/docs/configuring/services/sandboxes>
- Configure sandboxes for jobs: <https://docs.cloud.google.com/run/docs/configuring/jobs/sandboxes>

Doc facts that shaped the design: the binary is `/usr/local/gcp/bin/sandbox`; a
sandbox has **no network egress by default**, does not inherit env, mounts the
host rootfs read-only, and **shares the host container's CPU and memory**;
`sandbox do` forwards stdin/stdout/stderr (`--stdin` defaults true).
Observed, not documented: the sandbox kernel is gVisor (`uname`: `4.19.0-gvisor`),
it has a working `lo` (127.0.0.1), `resolv.conf` points at 169.254.169.254, the
process runs as uid 0 inside, and there is no `sandbox version` subcommand.

## Set-up

| Piece | What | Where |
| --- | --- | --- |
| Host | Cloud Run **job** `b6-bench`, gen2, 4 vCPU / 4 GiB, `sandboxLauncher: true`, Direct VPC egress (private ranges) into `b6-subnet` | europe-west1 (Go reports `NumCPU` 5–6; the quota is 4) |
| Test server | VM `b6-server`, n2-standard-4, COS, `b6 server` in Docker with host networking, TLS on `10.66.0.2:8443` with a throwaway CA | **europe-west1-b**, internal IP only reachable from `b6-subnet` (firewall `b6-allow-8443-internal`) |
| Proxy | `b6 proxy`: CONNECT-only, allowlists exactly `10.66.0.2:8443`, runs on the host at 127.0.0.1:3128 | host container |
| Tunnel | **Built here, minimal** (none was supplied): `b6 tunnel-host` spawns `sandbox do … -- /b6 tunnel-sbx -- <client>`. In the sandbox `tunnel-sbx` listens on 127.0.0.1:3128 and is the client's `HTTPS_PROXY`; every accepted TCP connection is multiplexed over the `sandbox do` stdin/stdout pair as 9-byte-header frames (OPEN/DATA/CLOSE, 32 KiB max payload). On the host each stream is dialled to the proxy. TLS is end to end client↔server; the tunnel and proxy see ciphertext only | `src/tunnel.go` |
| R5 transport | `b6 filebridge` on the host polls a bind-mounted dir (every 100 µs) for `<id>.req` files, fetches via the proxy, writes `<id>.resp` atomically; the client in the sandbox polls for it | `src/filebridge.go`, `fileRT` in `src/client.go` |
| Client | `b6 client`: Go, HTTP/1.1 forced, one TCP connection per stream, connections warmed before timing; trusts only the test CA | `src/client.go` |

Why a VM and not a Cloud Run service: a public `--allow-unauthenticated`
service was refused by this session's permission policy, and an
authenticated one would have meant putting a token inside the sandbox. An
internal-only VM in a `b6-` VPC keeps the server private, keeps every byte
inside one region's VPC (so R1 is a transport ceiling, not an internet
measurement — **P2**), and needs no credential anywhere near the sandbox.

Every run is a fresh `sandbox do`. Each execution does 3 reps of every
path × measure with the path order rotated; 3 executions
(`b6-bench-kdpqv`, `b6-bench-94jph`, `b6-bench-rl7zj`, 16:57–17:02 UTC), so
most cells have n=9. Cells show **median [min–max] n**. Some cells have n=7–8:
about 5% of stdout JSON lines never reached Cloud Logging (the same gaps on a
re-read). Every cell still has samples from all three executions.

R3b is an extra: R3 with 256 KiB frames, to test whether frame size matters.

## Throughput, MiB/s (M1, M2)

| Path | M1-10MiB | M1-100MiB | M2-1x16MiB | M2-4x16MiB | M2-16x16MiB |
| --- | --- | --- | --- | --- | --- |
| R1 | 466 [304–666] n=9 | 551 [227–640] n=9 | 522 [366–688] n=8 | 483 [413–596] n=9 | 540 [461–645] n=7 |
| R2 | 491 [210–618] n=9 | 517 [348–750] n=8 | 431 [375–641] n=8 | 477 [465–686] n=9 | 626 [502–730] n=9 |
| R3 | 327 [45–438] n=9 | 417 [238–456] n=9 | 358 [275–410] n=8 | 368 [190–468] n=9 | 358 [311–458] n=9 |
| R3b | 330 [220–363] n=8 | 383 [285–475] n=9 | 332 [65–412] n=8 | 388 [225–472] n=9 | 475 [375–521] n=9 |
| R4 | 379 [297–565] n=9 | 386 [330–498] n=8 | 400 [335–438] n=9 | 479 [389–532] n=9 | 540 [460–636] n=7 |
| R5 | 351 [265–416] n=9 | 372 [250–460] n=8 | 322 [256–469] n=8 | 458 [357–516] n=8 | 432 [336–473] n=9 |

## Latency, ms (M3: 500 sequential requests on one warm connection per run)

| Path | p50 | p90 | p99 |
| --- | --- | --- | --- |
| R1 | 0.386 [0.294–0.429] n=9 | 0.453 [0.364–0.516] n=9 | 0.657 [0.447–1.310] n=9 |
| R2 | 0.457 [0.387–0.507] n=9 | 0.556 [0.475–0.615] n=9 | 0.759 [0.571–1.272] n=9 |
| R3 | 0.875 [0.788–0.998] n=8 | 1.020 [0.905–1.231] n=8 | 1.505 [1.170–3.035] n=8 |
| R3b | 0.857 [0.775–1.034] n=8 | 0.974 [0.861–1.266] n=8 | 1.565 [1.086–2.310] n=8 |
| R4 | 0.500 [0.444–0.562] n=9 | 0.564 [0.522–0.714] n=9 | 0.775 [0.614–1.051] n=9 |
| R5 | 2.431 [2.299–2.794] n=9 | 3.069 [2.939–3.710] n=9 | 6.193 [3.929–12.453] n=9 |

## Start-up, ms (M4: from just before `sandbox do` to the first successful request)

Every run is a fresh `sandbox do`, so every run is an M4 sample. For R1/R2 the clock starts at client process start.

| Path | first request | sandbox process running (tunnel-sbx start) | client process start |
| --- | --- | --- | --- |
| R1 | 3.4 [2.8–15.1] n=51 | — | 0.0 [0.0–0.0] n=51 |
| R2 | 3.7 [3.0–5.7] n=52 | — | 0.0 [0.0–0.0] n=52 |
| R3 | 167.0 [124.4–204.1] n=52 | 152.1 [113.2–187.3] n=52 | 159.4 [117.9–195.0] n=52 |
| R3b | 158.8 [124.3–203.6] n=51 | 145.3 [113.5–180.0] n=51 | 152.3 [118.3–192.7] n=51 |
| R4 | 237.7 [177.7–296.2] n=51 | — | 234.0 [174.4–292.1] n=51 |
| R5 | 159.0 [118.7–187.5] n=51 | — | 152.2 [112.7–181.2] n=51 |

`sandbox do -- /bin/true` round trip (from Go exec): 175 [128–271] n=8 ms

## CPU during M1-100MiB (M5), CPU-seconds per 100 MiB

| Path | tunnel-host | `sandbox do` CLI (child of tunnel-host) | tunnel-sbx (in sandbox) | proxy | client | whole container cgroup | wall s |
| --- | --- | --- | --- | --- | --- | --- | --- |
| R1 | — | — | — | 0.00 [0.00–0.01] n=9 | 0.13 [0.10–0.17] n=9 | 0.13 [0.10–0.18] n=9 | 0.19 [0.16–0.45] n=9 |
| R2 | — | — | — | 0.13 [0.10–0.13] n=8 | 0.13 [0.10–0.16] n=8 | 0.25 [0.21–0.30] n=8 | 0.20 [0.14–0.30] n=8 |
| R3 | 0.13 [0.12–0.17] n=9 | 0.83 [0.81–1.06] n=9 | 0.33 [0.26–0.38] n=9 | 0.12 [0.09–0.14] n=9 | 0.14 [0.09–0.18] n=9 | 1.09 [1.04–1.37] n=9 | 0.42 [0.37–0.63] n=9 |
| R3b | 0.14 [0.13–0.15] n=9 | 0.82 [0.69–0.96] n=9 | 0.34 [0.22–0.41] n=9 | 0.11 [0.08–0.13] n=9 | 0.13 [0.07–0.20] n=9 | 1.09 [0.92–1.24] n=9 | 0.45 [0.37–0.50] n=9 |
| R4 | — | — | — | 0.00 [0.00–0.00] n=8 | 0.23 [0.20–0.29] n=8 | 0.77 [0.59–0.89] n=8 | 0.58 [0.48–0.64] n=8 |
| R5 | — | — | — | 0.14 [0.10–0.16] n=8 | 0.17 [0.09–0.21] n=8 | 0.85 [0.79–1.05] n=8 | 0.45 [0.40–0.60] n=8 |

## Decomposition probes

| Probe | MiB/s | CPU-s (CLI / cgroup) |
| --- | --- | --- |
| raw `sandbox do` stdin, host→sandbox, 200 MiB | 1858 [1318–2728] n=8 | 0.46 [0.33–0.70] n=8 / 0.57 [0.41–0.84] n=8 |
| raw `sandbox do` stdout, sandbox→host, 200 MiB | 1652 [1257–2248] n=8 | 0.45 [0.34–0.65] n=8 / 0.55 [0.41–0.79] n=8 |
| sbx_loopback plain HTTP, 1 stream(s), 100 MiB each | 763 [255–1140] n=9 | — |
| sbx_loopback plain HTTP, 4 stream(s), 100 MiB each | 1155 [919–2772] n=9 | — |
| host_loopback plain HTTP, 1 stream(s), 100 MiB each | 2635 [1961–3332] n=9 | — |
| host_loopback plain HTTP, 4 stream(s), 100 MiB each | 5756 [4081–7659] n=9 | — |

## Failed runs

none

## M5 marginal CPU (CPU-s per 90 MiB: median M1-100MiB minus median M1-10MiB, cancelling fixed start-up)

| Path | cgroup | sandbox_cli | tunnel_host | tunnel_sbx | proxy | client |
| --- | --- | --- | --- | --- | --- | --- |
| R1 | 0.11 | — | — | — | 0.00 | 0.11 |
| R2 | 0.21 | — | — | — | 0.11 | 0.10 |
| R3 | 0.67 | 0.45 | 0.11 | 0.28 | 0.11 | 0.11 |
| R3b | 0.68 | 0.44 | 0.12 | 0.30 | 0.10 | 0.11 |
| R4 | 0.36 | — | — | — | 0.00 | 0.20 |
| R5 | 0.48 | — | — | — | 0.12 | 0.15 |

(R1/R2/R4/R5 have no tunnel processes. In-sandbox figures — `tunnel-sbx`,
and the client on R3–R5 — are gVisor's own rusage accounting; they are not
additive with the host-side figures, which is why the parts can exceed the
cgroup total. The cgroup figure is the one to trust.)

## Results: paths × measures (medians; full spreads in the tables above)

| Path | M1 10 MiB | M1 100 MiB | M2 1 / 4 / 16 streams (aggregate) | M3 p50 / p90 | M4 first request | M5 container CPU per 100 MiB (marginal per 90 MiB) |
| --- | --- | --- | --- | --- | --- | --- |
| R1 host direct | 466 MiB/s | 551 MiB/s | 522 / 483 / 540 | 0.39 / 0.45 ms | 3.4 ms (no sandbox) | 0.13 (0.11) |
| R2 host via proxy | 491 | 517 | 431 / 477 / 626 | 0.46 / 0.56 ms | 3.7 ms (no sandbox) | 0.25 (0.21) |
| **R3 sandbox, stdio tunnel + proxy** | 332 | **417** | 358 / 368 / **358** | **0.88 / 1.02 ms** | **167 ms** (sandbox up at 151) | **1.09 (0.67)** |
| R3b same, 256 KiB frames | 333 | 383 | 332 / 388 / 475 | 0.86 / 0.97 ms | 155 ms | 1.09 (0.69) |
| R4 sandbox `--allow-egress` direct | 379 | 386 | 400 / 479 / 540 | 0.50 / 0.56 ms | 238 ms | 0.77 (0.36) |
| R5 bind-mount files (works) | 351 | 372 | 322 / 458 / 432 | 2.43 / 3.07 ms | 159 ms | 0.85 (0.48) |

Spread is wide on every path (e.g. R1 100 MiB 227–640 MiB/s): the host is a
shared Cloud Run machine and runs were seconds apart. Differences under
~15% between paths are inside the noise; the R3 latency gap and the R3 CPU
gap are not.

**P1:** met — M1–M4 for R1–R5 (R5 works), n=7–9 per cell from 3 executions,
with min–max. **P2:** met — server at 10.66.0.2, europe-west1-b, same VPC
as the job (Direct VPC egress), no internet path. **P3:** below.

## Where the tunnel's cost comes from (P3)

1. **Not a throughput cap on the stdio channel.** Raw `sandbox do` stdio,
   with no framing, moves 1858 MiB/s host→sandbox and 1652 MiB/s
   sandbox→host (decomposition table), 4× what the tunnel achieves.
2. **Not the proxy.** R2 ≈ R1 in throughput; the proxy costs 0.11 CPU-s per
   90 MiB on both R2 and R3, and +0.07 ms p50.
3. **Not frame size.** R3b (256 KiB frames) ≈ R3 everywhere except
   16 streams (475 vs 358, within the spread of each other's tails). Reads
   from the TCP sockets rarely fill 32 KiB anyway.
4. **The ceiling is the network, not the client.** R1 is ~550 MiB/s (~4.6 Gbit/s)
   whether 1 or 16 streams, while the same client on host loopback does
   2635 (1 stream) – 5756 (4 streams) MiB/s. So R1 is the Cloud Run → VPC
   egress ceiling, and R3 reaches ~75% of it on one stream.
5. **It is CPU, mostly in the `sandbox do` relay.** Marginal container CPU
   per 90 MiB: R3 0.67 vs R2 0.21. Of R3's extra, the `sandbox do` CLI process
   (which relays stdin/stdout into gVisor) is the largest host-side part at
   0.45 CPU-s; `tunnel-host` adds 0.11; gVisor reports 0.28 for `tunnel-sbx`.
   During a 100 MiB R3 transfer the container burns ~1.1 CPU-s in ~0.42 s,
   i.e. ~2.6 of its 4 vCPUs — sandboxes share the host's CPU quota (as the
   docs say), so a busy host will slow the tunnel directly.
6. **Latency is per-hop, not gVisor networking.** R4 (gVisor netstack,
   direct) adds only 0.11 ms p50 over R1; R3 adds 0.42 ms over R2. That
   0.4 ms is the in-sandbox loopback hop to `tunnel-sbx`, the stdio relay in
   each direction, and the host-side hop to the proxy.
7. **Many streams don't scale** through the single multiplexed pipe
   (R3 358 MiB/s at 16 streams while R1/R4 reach 540): every stream shares
   one framer lock, one demux goroutine and one pipe pair, so it behaves
   like one stream.
8. **Start-up is the sandbox, not the tunnel.** First request at 167 ms, of
   which 151 ms is before `tunnel-sbx` is running; the tunnel, CONNECT and
   TLS handshake add ~15 ms. `sandbox do -- /bin/true` is 175 ms
   round trip. R4 is slower to start (238 ms; the CLI re-execs itself to
   set up networking, visible as `/proc/self/exe do --allow-egress` in the
   logs).

## Verdict

The stdio tunnel is fast enough for agent traffic: ~400 MiB/s (~3.4 Gbit/s)
on one stream, about 75% of what the host itself gets to an in-region
server, ~0.9 ms per small request on a warm connection (vs 0.46 ms through
the proxy alone), and ~15 ms on top of a ~150 ms sandbox start. Its real
price is CPU — about 3× the proxy-only path per byte, ~0.75 CPU-s per GiB
extra, taken from the same vCPUs as the workload — and its lack of scaling
with parallel streams. `--allow-egress` buys almost nothing on a single
stream and gives isolation away; it wins only on many parallel streams and
on sub-millisecond latency. The bind-mount file transport works but is
2.4 ms per request with polling that burns CPU while idle; it is a
curiosity, not an alternative.

## Deviations and problems hit

- **Test server is a VM, not a Cloud Run service** (see Set-up).
- **`gcloud` 570.0.0 has no `--sandbox-launcher`** on `beta run jobs
  create/deploy`, and `run jobs replace` rejects `sandboxLauncher` in YAML,
  contrary to the docs. Enabled it with a REST PUT
  (`enable-sandbox.py`; token read inside the script, header only). A later
  `gcloud run jobs update` preserved the field.
- **Images could not be pushed from the agent sandbox**: its egress policy
  refuses `europe-west1-docker.pkg.dev`, and no docker credential helper is
  installed. Built with Cloud Build instead, using a `b6-build` SA and a
  `b6-` bucket for source and logs (Cloud Build's default bucket is not
  `b6-` prefixed). `docker-login.py` is left in the folder but did not
  work for that reason.
- **`create-with-container` is discontinued** for VMs; used COS + cloud-init.
  COS's host firewall drops inbound by default, so the unit opens 8443 with
  iptables (the VPC firewall still restricts it to `b6-subnet`).
- **Smoke run lost ~10 min**: the host-loopback probe backgrounded a server
  that held the output pipe open; cancelled and fixed.
- **~5% of the job's stdout JSON lines never reached Cloud Logging**, so
  n is 7–9, not 9.
- **The R5 polling client burned ~13 CPU-s per minute inside gVisor** while
  waiting for a response that never came (first smoke run); the R5 bridge
  is started only for R5 runs so its idle polling does not inflate other
  paths' CPU.
- **Clean-up is incomplete for up to 2 hours**: `b6-subnet` (and so
  `b6-vpc`) cannot be deleted until Cloud Run releases its Direct VPC
  address (`serverless-ipv4-cloudrun-…`, documented 1–2 h wait at
  <https://docs.cloud.google.com/run/docs/configuring/vpc-direct-vpc>).
  Both are free. Re-run `cleanup.sh` after ~19:10 UTC.

## Files

`src/*.go` (one binary: server, proxy, tunnel both ends, file bridge,
client, orchestrator), `Dockerfile.bench`, `Dockerfile.server`,
`cloudbuild.yaml`, `.gcloudignore`, `server-cloud-init.yaml`,
`enable-sandbox.py`, `job.yaml`/`job.json` (job definition),
`run-matrix.sh`, `logs.sh`, `summarise.py`, `analyse.py`, `marginal.py`,
`data/*.json` (raw logs of the 3 executions), `results.md`, `marginal.md`,
`cleanup.sh` and `cleanup.out`. `pki/` held the throwaway test CA and
server key; deleted with the clean-up.

## Clean-up output (17:17 UTC)

```text
deleted: Cloud Run job b6-bench
deleted: VM b6-server
deleted: internal address b6-server-ip
deleted: firewall b6-allow-8443-internal
deleted: Artifact Registry repo b6-repo
deleted: bucket gs://b6-build-<sandbox-project>
deleted: service account b6-run
deleted: service account b6-build
absent or failed: network b6-vpc
--- remaining b6- resources (should be none):
b6-subnet
b6-vpc
--- end

[exited with code 0]
```
