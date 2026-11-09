# sandbox-bridge rig

<!-- cspell:ignore gVisor yamux nip localtest behavior grns getent rootfs crun dmqrw mpnbt -->

Evidence rig for the article:
can an untrusted process in a Cloud Run sandbox with **no network** reach
**selected** hosts, and nothing else, through a proxy outside the sandbox?
No GitHub runner is involved, so the technique stands on its own.

**Experimental.** It's a test harness, not a product. It runs in a broker-created
sandbox project with a spend cap and TTL.

## What runs

One Cloud Run job execution (`sandboxLauncher: true`, 2 vCPU, 4 GiB). The
host container (`launcher.sh`) starts `egress-proxy` on a Unix socket, writes
host-filesystem canaries, then runs `tests.sh` in three contexts:

| Context | What it is | Why |
| --- | --- | --- |
| `sandbox` | Sandbox, no egress; out only via `stdio-bridge` → proxy | The configuration under test |
| `sandbox-allow-egress` | Same, with `--allow-egress` | Negative control: shows the direct-bypass tests can succeed |
| `host` | The host container, same bridge | Control: shows a proxy alone is advisory |

Then `channels.sh` probes which channels cross the host/sandbox boundary.

| File | Role |
| --- | --- |
| `proxy/`, `bridge/` | `egress-proxy` and `stdio-bridge`, **copied unchanged** from the production runner image, so the rig tests what production runs |
| `probe/` | A Go client, so results aren't curl-only |
| `tests.sh`, `inside.sh`, `channels.sh`, `launcher.sh` | The tests and their wiring |
| `Dockerfile`, `cloudbuild.yaml`, `allowlist.txt` | The image |
| `setup.sh`, `job.yaml.tmpl`, `crun.py`, `run.sh` | Project setup and running. `crun.py` calls the Admin API because gcloud 570.0.0 rejects `sandboxLauncher` |
| `results/` | Raw log exports, one file per execution |

## Reproduce

```sh
PROJECT=<sandbox-project> ./setup.sh
gcloud builds submit --project "$PROJECT" --region europe-west1 --config cloudbuild.yaml \
  --substitutions _IMAGE=europe-west1-docker.pkg.dev/$PROJECT/rig/sandbox-bridge:v1 \
  --service-account "projects/$PROJECT/serviceAccounts/rig-build@$PROJECT.iam.gserviceaccount.com" \
  --default-buckets-behavior=regional-user-owned-bucket .
PROJECT=<sandbox-project> IMAGE=europe-west1-docker.pkg.dev/<sandbox-project>/rig/sandbox-bridge:v1 ./run.sh all
```

One run takes about 90 s of execution and costs well under 1p in compute.

## Results, run 1: 2026-11-07, execution `grns7`

Ubuntu 26.04.1 image; host kernel 6.9.12; sandbox kernel `4.19.0-gvisor`;
curl 8.18.0; OpenSSL 3.5.5. Raw log: `results/2026-11-07-grns7.jsonl`.

### Egress matrix

✅ = reached; ❌ = refused or no route. **Bold** = the result the design depends on.

| Check | sandbox | allow-egress | host |
| --- | --- | --- | --- |
| Allowlisted host via proxy (curl, Go) | ✅ 200 | ✅ | ✅ |
| Other host via proxy (curl, Go) | **❌ 403** | ❌ 403 | ❌ 403 |
| Direct, ignoring proxy (curl, Go) | **❌ no DNS** | ✅ 200 | ✅ |
| Direct IPv4 literal (curl, Go dial) | **❌ unreachable** | ✅ | ✅ |
| Direct IPv6 literal | **❌** | ❌ (no IPv6 route) | ✅ |
| DNS lookup (getent, Go) | **❌** | ✅ | ✅ |
| Metadata server direct, IP and name (curl, Go) | **❌ unreachable** | **❌ timeout** | ✅ 200 |
| Metadata via proxy, IP and name | ❌ 403 | ❌ 403 | ❌ 403 |
| IP literal via proxy | ❌ 403 | ❌ 403 | ❌ 403 |
| Port 22 via proxy | ❌ 403 | ❌ 403 | ❌ 403 |
| Allowlisted name → 127.0.0.1 (`localtest.me`) | ❌ 403 | ❌ 403 | ❌ 403 |
| Allowlisted name → 169.254.169.254 (`*.nip.io`) | ❌ 403 | ❌ 403 | ❌ 403 |
| CONNECT `api.github.com`, TLS SNI `example.com` | ⚠️ allowed | ⚠️ allowed | ⚠️ allowed |
| 50 MB download via bridge | 110 MB/s | 109 MB/s | 76 MB/s |

- The `sandbox` and `host` columns together reproduce spike S2. The middle
  column is the control that proves the bypass tests can pass.
- **Metadata stays unreachable even with `--allow-egress`.** That matches S2.
- **The fronting row:** the proxy filters only the CONNECT host. A TLS
  handshake naming a different server completes through it. Hostname
  filtering without TLS inspection cannot prevent domain fronting through a
  shared front end. That is a documented limitation, not a bug.
- Not tested live: an allowlisted name with a **mixed** public and private
  DNS answer. The code dials the public address rather than refusing.
  It needs a DNS name the rig controls, or a unit
  test with a stub resolver.

### Host filesystem visibility (new; found by the prompt 5 threat model)

| Host wrote at run time, before the sandbox started | Sandbox could read it |
| --- | --- |
| `/etc/host-canary`, mode 0600 root | **yes**, with and without `--allow-egress` |
| `/tmp/host-canary` | no |
| A host environment variable | no (not inherited) |
| Image files (`/etc/egress-proxy/allowlist.txt`) | yes |

The sandbox runs as uid 0, so file modes don't protect host files. Any
secret the host writes outside `/tmp` is readable by the untrusted process.
Followed up in the production runner.

### Boundary channels (repeats spike Finding 1)

| Channel | Result |
| --- | --- |
| Sandbox connects to a host Unix socket on a bind mount | ❌ `Connection refused` (the socket is visible: `socket 666 root`) |
| Host connects to a socket the sandbox created | ❌ `No such file or directory` |
| FIFO on the mount, sandbox reading or creating | ❌ `Permission denied` / `Operation not permitted` |
| Host loopback seen from the sandbox | ❌ not shared |
| Sandbox stdin/stdout, 1 MiB random | ✅ identical SHA-256 |
| Sandbox stdin/stdout, interactive | ✅ a line answered before stdin closed; the second arrived 4 s later, as sent |

Mounts in the sandbox show as gVisor 9p (`trans=fd ... disable_fifo_open`),
as in the spike. **Anomaly to re-check:** the host-side FIFO writer exited 0
this time. In the spike, it failed. The sandbox's open still failed, so the
conclusion is unchanged, but the writer result is unexplained.

## Results, round 2: 2026-11-07, executions `4c89n`, `7rph4`, `dmqrw`, `mpnbt`

Image v2 for the three repeat runs, v3 for the `--rootfs` probe. Sandbox CLI
binary `sha256:f1f88a5625618d89…`, 57,516,864 bytes. The CLI documents no
version command, so the checksum identifies it.

**Every check gave the same outcome in all three repeat runs.** Run 1's
matrix above holds as stated, now at n=4.

| Question | Answer |
| --- | --- |
| Which host paths can the sandbox read? | Everything tried except `/tmp`: `/etc`, `/root`, `/var/tmp`, `/opt/rig`, `/home`. The CLI reference: "only `/tmp` is writable in memory", the sandbox's own |
| Live view or snapshot? | **Snapshot at sandbox start.** A file the host wrote 3 s after `sandbox do` started was never seen within 10 s, in all three runs |
| Allowlisted name with a **mixed** answer, `[10.0.0.5, 1.1.1.1]` | **`connect=200`**: the proxy skips the private address and dials the public one. Confirmed live |
| Allowlisted name, private-only answer | `connect=403`, refused |
| Regular file on a bind mount | **Crosses both ways**, host to sandbox and back. A possible, slower, channel |
| `--rootfs` with a clean root | **Fails in every form tried**: `FetchSpec failed: loading container: file does not exist`, about 70 ms, exit 1. Only `--rootfs /` works. Misuse or preview bug, unknown |
| Throughput, one 50 MB download through the bridge | 79–117 MB/s from the sandbox over four runs. The host via the same bridge ranged 34–127 MB/s, so the remote dominates. Quote as "roughly 80–120 MB/s, noisy" |

The mixed and private-only names come from host `/etc/hosts` entries. The
proxy uses Go's resolver, which reads that file first, so the DNS answer is
deterministic without a domain the rig controls.

## Next for the rig

- `--rootfs`: ask, or wait for the feature to change. Two rounds of variants
  is enough guessing.
- The host-side FIFO writer still exits 0 while the sandbox side fails.
  It's unexplained, and the conclusion doesn't depend on it.
- Throughput against a server in the same region, to measure the bridge
  rather than the internet.
