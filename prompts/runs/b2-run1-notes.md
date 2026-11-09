# Cloud Run sandbox: which channels cross the boundary

Probe run 2026-11-07, 21:05–21:31 UTC, project `<sandbox-project>`, region
`europe-west1`, Cloud Run **job** `b2-probe` (gen2, 2 vCPU / 2 GiB,
`sandboxLauncher: true`, identity `b2-job` with no roles). Image
`python:3.12-slim` + `sudo` + `procps` + `host.py` + `side.py`.

## Docs read (retrieved 2026-11-07)

- <https://docs.cloud.google.com/run/docs/code-execution>
- <https://docs.cloud.google.com/run/docs/reference/sandbox-cli>
- <https://docs.cloud.google.com/run/docs/configuring/jobs/sandboxes>

### What the docs promise

- Egress blocked by default; `--allow-egress` lifts it. No host env vars,
  secrets or metadata-server credentials are inherited.
- `--rootfs` (default `/`) exposes the host container's filesystem read-only;
  "Don't store sensitive files on the host container's disk where sandboxed
  processes can read them."
- `--mount type=bind,src=,dst=[,readonly]` is read-write by default, rejects
  `..` and "sensitive system paths".
- `/tmp` in the sandbox is a separate in-memory fs, excluded from tars.
- `--stdin`/`--stdout`/`--stderr` forward the caller's streams (default on).
- `--import-tar`/`--export-tar`/`--sync-tar`, `sandbox tar`, `sandbox fork`
  move the rootfs-upper overlay as a tar.
- "Processes run with `sudo` privileges as a non-root user."
- The CLI exits 0 on success, 1 on any CLI error **or** any non-zero command
  status.

### What the docs don't say

- Anything about sockets, FIFOs, or what kind of filesystem a bind mount is
  (it turns out to be gVisor 9p with `disable_fifo_open`).
- Whether a file written on the host after start is visible (it is).
- Whether stdout streams before stdin EOF (it does).
- What the tar format for `--import-tar` must be (a root `./` entry appears
  to be required; without it you get a misleading error, below).
- What happens when the `--mount` source doesn't exist (same misleading
  error).
- That every `sandbox` command line, `--env` values included, is written to
  the container's log as `[start]`/`[end]` lines (observed).
- Docs example spells the mount `source=`/`destination=`; the reference says
  `src=`/`dst=`. Both work (probe `altmount`).
- gcloud 570.0.0 (beta 2026.05.22) has no `--sandbox-launcher` flag and
  `gcloud run jobs replace` rejects `sandboxLauncher` in YAML; the job had to
  be created through the Admin API (`runapi.py`).

## Method

`host.py` is the job's entrypoint (trusted). For every channel it runs
`side.py <probe>` twice: once with `sandbox do` (**sbx**) and once as a
plain host child process (**ctl**, the host-to-host control required by P3).
Each side generates a fresh `secrets.token_hex(16)` token per run; the host
token goes one way, the side's token the other, and a direction PASSES only
if the exact token arrives (P2). Tokens never go on a sandbox command line,
except the `--env` probe, whose token is deliberately non-secret.

Bind mount: host `/srv/b2share` (mode 0777) → sandbox `/mnt/share`.

Seven executions (P4): `65f4f` (image v1), `599wd`, `mhlk4`, `s5wrw` (v2),
`746ck`, `867qw`, `x8qbg` (v3). v2 added: retry-up-to-3 on platform errors,
H5 "rewrite after start", socket stat. v3 added: warm-up timing, two more
`--import-tar` variants.

## Results: channel × direction

"sbx" = through the sandbox boundary; "ctl" = same probe host-to-host.
Counts are passes / executions that ran that probe.

| # | Channel | Direction | sbx | ctl | Exact error (sbx) |
| --- | --- | --- | --- | --- | --- |
| H1 | Unix socket, host listens in bind dir | host→sbx | 0/7 | 7/7 | `ConnectionRefusedError: [Errno 111] Connection refused` (socket file *is* visible, `mode=140777`) |
| H1 | " | sbx→host | 0/7 | 7/7 | same (no connection) |
| H2 | Unix socket, sandbox listens in bind dir | sbx→host | 0/7 | 7/7 | sandbox `bind/listen` succeeds, but host sees `ls=[]`: socket path absent on host |
| H2 | " | host→sbx | 0/7 | 7/7 | sandbox `accept`: `TimeoutError: timed out` |
| H3 | FIFO host→sbx | host→sbx | 0/7 | 7/7 | sandbox `open`: `PermissionError: [Errno 1] Operation not permitted: '/mnt/share/h3_h2s.fifo'` |
| H3 | FIFO sbx→host | sbx→host | 0/7 | 7/7 | `PermissionError: [Errno 1] Operation not permitted: '/mnt/share/h3_s2h.fifo'` |
| H4 | TCP 127.0.0.1, host listens | host→sbx / sbx→host | 0/7 | 7/7 | `ConnectionRefusedError: [Errno 111]` |
| H4 | TCP 127.0.0.1, sandbox listens | host→sbx / sbx→host | 0/7 | 7/7 | host: `ConnectionRefusedError: [Errno 111]`; sandbox: `accept` timeout |
| H4+ | TCP to host's own IP 169.254.8.1, host on 0.0.0.0 | both | 0/7 | 7/7 | `OSError: [Errno 101] Network is unreachable` |
| H5 | File, written before start | host→sbx | 7/7 | 7/7 | — |
| H5 | File, **new** file written 3 s after start | host→sbx | 7/7 | 7/7 | — |
| H5 | File, existing file **rewritten** after start | host→sbx | 6/6 | 6/6 | — (not in v1) |
| H5 | File, read by host after sandbox exit | sbx→host | 7/7 | 7/7 | — |
| H5 | File, read by host **while sandbox still running** | sbx→host | 7/7 | 7/7 | — |
| H6 | `sandbox do` stdin, two request/response rounds | host→sbx | 7/7 | 7/7 | — |
| H6 | `sandbox do` stdout, reply before stdin EOF | sbx→host | 7/7 | 7/7 | — (RTT ≈ 0.2 s incl. sandbox start) |
| H7 | `/etc/b2-h7.txt` mode 0600 root, written before start | host→sbx read | 7/7 | 7/7 | **readable** |
| H7 | `/root/b2-h7.txt` mode 0600 root | host→sbx read | 7/7 | 7/7 | **readable** |
| H7 | `/etc/b2-h7-world.txt` mode 0644 | host→sbx read | 7/7 | 7/7 | readable |
| H8 | Abstract Unix socket, host listens | both | 0/7 | 7/7 | `ConnectionRefusedError: [Errno 111]` |
| H8 | Abstract Unix socket, sandbox listens | both | 0/7 | 7/7 | host `ConnectionRefusedError`; sandbox `accept` timeout |
| H8 | `/dev/shm` file | host→sbx | 0/7 | 7/7 | `FileNotFoundError: [Errno 2] ... '/dev/shm/b2-h2s'` |
| H8 | `/dev/shm` file | sbx→host | 0/7 | 7/7 | host: `FileNotFoundError ... '/dev/shm/b2-s2h'` (sandbox write succeeded into its own tmpfs) |
| H8 | `--env KEY=VALUE` | host→sbx | 7/7 | 7/7 | — (but value is logged) |
| H8 | exit status 42 | sbx→host | 0/7 as rc | 7/7 | CLI returns 1; status only in stderr text `Error: failed to exec in container: cmd.Wait(exec) failed: exit status 42` |
| H8 | `--write --export-tar`, host reads member without extracting | sbx→host | 7/7 | 7/7 | — |
| H8 | `--import-tar`, host-built tar **without** `./` entry | host→sbx | 0/7 | 7/7 | `FetchSpec failed: loading container: file does not exist` / `exit status 128` (3 attempts each run) |
| H8 | `--import-tar`, host-built tar **with** `./` entry (GNU) | host→sbx | 3/3 | 3/3 | — |
| H8 | `--import-tar` of a platform-exported tar | round trip | 3/3 | n/a | — |
| H8 | `sandbox run --detach` + `sandbox exec` stdin/stdout, 2 rounds | both | 6/7 | n/a | 1 "fail" is a v1 driver bug (`flush of closed file` after the bytes had been seen correctly) |
| H8 | TCP to 169.254.169.254:80 (metadata) and 8.8.8.8:53 | sbx→net | 0/7 | 7/7 | `OSError: [Errno 101] Network is unreachable` |

Every result repeated identically across all executions that ran it (P4).
The only non-repeat is the H8 exec row, explained above.

### Evidence pointers

- Per-execution results: `evidence/<execution>.jsonl` (one JSON object per
  probe × mode, with every token sent and received).
- Platform lines (`[start]`/`[end]` per sandbox command): `evidence/*.other.txt`.
- Full table per execution: `table.md` (generated by `analyse.py`).

### `/proc/mounts` inside the sandbox (identical every run)

```text
none / 9p ro,trans=fd,rfdno=4,wfdno=4,aname=/,dfltuid=4294967294,dfltgid=4294967294,dcache=1000,cache=fscache,disable_fifo_open,overlayfs_stale_read,directfs 0 0
none /dev dev rw,mode=0755 0 0
none /sys sysfs ro,nosuid,noexec,dentry_cache_limit=1000 0 0
none /tmp tmpfs rw,mode=1777 0 0
none /proc proc rw,dentry_cache_limit=1000 0 0
none /dev/pts devpts rw 0 0
none /mnt/share 9p rw,trans=fd,rfdno=5,wfdno=5,aname=/,dfltuid=4294967294,dfltgid=4294967294,dcache=1000,cache=remote_revalidating,disable_fifo_open,overlayfs_stale_read,directfs 0 0
none /tmp/runsc-root tmpfs rw 0 0
none /tmp/bundle-root tmpfs rw 0 0
```

The bind mount is a gVisor 9p/gofer mount with `cache=remote_revalidating`
(why late and rewritten files are seen) and `disable_fifo_open` (why FIFOs
give EPERM). Sockets are not passed through the gofer, so a socket file is a
dead inode on the other side. The host's own view is a gVisor sandbox too
(`overlay` root, `runsc`/`sandbox` binaries bind-mounted from `/dev/root`).

Other facts from `info_end`: sandbox kernel `4.19.0-gvisor`, hostname
`sandbox-<name>`, only `lo` in its network namespace (host has `ipvlan-eth0/1`),
own PID namespace (PID 1 = `sandbox default-entrypoint`; host processes not
visible), env = `HOME LC_CTYPE PATH` + what `--env` passes. `id` inside is
`uid=0(root)`, yet `sudo -n` fails with
`sudo: PERM_SUDOERS: setresuid(-1, 1, -1): Operation not permitted`.

## H7: what it means for secrets

Any file in the host container's filesystem is readable from the sandbox
**regardless of mode**: the sandboxed process is uid 0 and the host files
were root-owned 0600 in both `/etc` and `/root`. File permissions are no
defence. A secret the trusted process writes to disk outside `/tmp` — a
token file, a credentials JSON, `~/.config/...` — is readable by untrusted
code in every default sandbox (`--rootfs` defaults to `/`). Implications:

- keep secrets in memory only, or under the host's `/tmp`, which the sandbox
  may not see (its `/tmp` is its own tmpfs) — **not probed**, and the docs'
  own "Default read-only access" example runs a script from the host's
  `/tmp/my-scripts` inside the sandbox, which contradicts `/proc/mounts`.
  Probe this before relying on it;
- or pass `--rootfs` pointing at a directory with no secrets;
- never bake secrets into the image (the image *is* the rootfs).

## Two misleading platform errors

`FetchSpec failed: loading container: file does not exist` (exit 128) came
from two causes:

1. `--import-tar` with a tar that has no `./` root entry (consistent, 7/7;
   adding `./` fixed it 3/3).
2. The first `info` probe in every run, which ran before the mount source
   `/srv/b2share` had been created; the identical probe later in the run
   (`info_end`) passed 7/7, and v3's warm-up (`sandbox do -- /bin/true`, no
   mount) passed on its first try at 0.14 s uptime, ruling out launcher
   start-up. Inference: a missing `--mount` source gives this error.

## Recommendation

Build the transport on **H6: the sandboxed process's stdin/stdout**
(`sandbox do`, or `sandbox exec` into a detached sandbox).

- It is the only channel that is bidirectional, streaming and ordered with
  no filesystem semantics: two interleaved request/response rounds worked
  before stdin EOF in every run, with sub-second latency.
- It needs no mount, so the sandbox can run with no writable host directory
  at all — the smallest attack surface.
- It carries no secret on a command line and nothing is written to disk.
- stderr remains free for diagnostics; frame messages (length-prefixed or
  newline-delimited JSON) since it is a raw byte pipe.

Fallback / bulk data: **H5, regular files in a bind mount**, both directions,
including live visibility while the sandbox runs. It works but needs
polling or rename-based handoff (no sockets, no FIFOs, no inotify tested),
and a writable host directory the untrusted side can fill. `--export-tar` is
a one-shot sbx→host result channel but needs `--write` and a
"never-extract" reader.

Not viable by default: every socket (path, abstract, TCP loopback, host IP),
FIFOs, `/dev/shm`.

## Spend and limits

~26 minutes elapsed. Seven job executions of ~3 min at 2 vCPU / 2 GiB plus
three small Cloud Builds: well under £1. No probe hit the three-attempt
limit except `--import-tar` without `./`, where the driver stopped after 3
platform errors per run as the rules require.

## Files

- `image/host.py`, `image/side.py`, `image/Dockerfile` — the probes
- `job.json` — the job definition actually used; `job.yaml` — same, gcloud form (rejected by gcloud 570)
- `cloudbuild.yaml`, `runapi.py` (Admin API client; reads the token inside the script)
- `fetchlogs.sh`, `analyse.py`, `table.md`, `evidence/`
- `cleanup.sh` and its output `cleanup.log`
