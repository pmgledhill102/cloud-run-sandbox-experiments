# B3: is the sandbox's stdio a usable transport?

Cold run, 2026-11-08 (16:16-16:35 UTC). Project `<sandbox-project>`, `europe-west1`.

## Docs read (2026-11-08)

- <https://docs.cloud.google.com/run/docs/reference/sandbox-cli> (`sandbox run/exec/do` flags, `--stdin/--stdout/--stderr`)
- <https://docs.cloud.google.com/run/docs/code-execution> ("the sandbox CLI writes stdout and stderr from sandboxed commands directly to the standard streams of the invoking process")
- <https://docs.cloud.google.com/run/docs/configuring/jobs/sandboxes> (`sandboxLauncher: true`, `gcloud beta run jobs create --sandbox-launcher`)
- <https://docs.cloud.google.com/run/docs/configuring/services/sandboxes>

Nothing in the docs speaks to buffering, backpressure, half-close or signal behaviour; all of that below is measured.

## Rig

- Cloud Run job `b3-stdio` (gen2, `sandboxLauncher: true`), image `mirror.gcr.io/library/python:3.12-slim`, 2 vCPU / 2 GiB, identity `b3-job@` (no roles).
- Host process = `harness.py`, delivered as a zlib+base64 env var (no image build). It spawns `/usr/local/gcp/bin/sandbox do -- /usr/local/bin/python3 -u -c <prog>` with `subprocess.Popen` pipes, i.e. the host owns stdin/stdout/stderr of the CLI.
- `make-job.sh` writes `job.yaml` and creates/updates the job via the v1 REST API (`rest.py`; token read inside Python from gcloud's `auth/access_token_file`, header only). **gcloud 570.0.0 cannot do this**: `jobs replace` rejects `sandboxLauncher` ("Failed to parse value(s) in protobuf") and `jobs create` has no `--sandbox-launcher` flag.
- `run-exec.sh` executes and pulls `B3RESULT` lines from Cloud Logging into `logs/`. `aggregate.py` -> `aggregate.txt`.
- Executions: `b3-stdio-xq4b2` (1 task, smoke: meta,S1,S2,S6,S8), `b3-stdio-xfphc` and `b3-stdio-9br7k` (3 parallel tasks each = 6 independent instances, full plan), `b3-stdio-qkwvs` and `b3-stdio-8fsj8` (S7F, see S7).

`sandbox do -- python3 -c pass` start+teardown: 106-175 ms.

## Measurements

### S1 binary-clean: YES

5 MiB (1 MiB urandom + every byte value 0x00-0xff x4096 + 3 MiB urandom) through an echo program. Six instances, all `equal: true`, e.g. task 0 of xfphc:

```text
bytes_in 5242880 bytes_out 5242880
sha_in  a91641f0dc8236da771e5d31de9c82d71016faf721e6313b0e058c8034f2f0de
sha_out a91641f0dc8236da771e5d31de9c82d71016faf721e6313b0e058c8034f2f0de
```

No tty translation: CR, LF, ^C, ^D, NUL all pass. **P2 control**: byte 123457 flipped before sending; echoed hash `7f2311a2...` != original `a91641f0...`, first differing offset 123457, and the echo equals the corrupted input (the comparison can fail, and the pipe faithfully carries the corruption).

### S2 interactive: YES

Line-echo program; reply 1 read while stdin still open (0.13-0.21 s after spawn, i.e. sandbox startup); 5 s pause; reply 2 RTT 0.3 ms, arrived 1.0 s before host closed stdin. Process exited 31 ms after close. Identical on all 6 instances plus the smoke run. No line-buffering or until-EOF batching by the CLI.

### S3 throughput (18 runs: 6 instances x 3 reps; MB = 10^6 B)

```text
size   direction  median  min  max
1MiB   in          357    249  413
1MiB   out         538    271 1118
1MiB   roundtrip   256     22  336
10MiB  in          321    126  454
10MiB  out         462    177  718
10MiB  roundtrip   203    111  330
100MiB in          324    265  378
100MiB out         496    323  626
100MiB roundtrip   208    175  236
```

Timed from the program's `ready` line (startup excluded). Python echo on both ends, so this is a lower bound on the pipe. The 22 MB/s minimum is one 1 MiB run hit by a ~25 ms stall (see S4 max). Instance-to-instance spread is visible: xfphc task 1 was consistently ~30% slower.

### S4 latency, 64-byte line ping-pong, 1000 msgs after 50 warm-up (17 runs)

```text
p50 across runs: median 153 us, min 116, max 176
p90 across runs: median 175 us, min 132, max 270
p99 per run 182-500 us; max per run 13-29 ms (every run has one or a few ~25 ms stalls)
```

One run (xfphc task 2 rep 1) is missing: its `B3RESULT` and `B3TIME` lines never reached Cloud Logging (see "Log loss" below), so n=17 not 18.

### S5 backpressure: writer BLOCKS; bounded ~64 KiB in flight; no drop

Sandboxed reader sleeps 15 s, then reads 64 KiB / 5 ms. Host writer does blocking writes of 128 MiB (64 KiB chunks in xfphc, 4 KiB chunks in 9br7k), sampled every second along with the RSS of the `sandbox` CLI process tree:

```text
t=1..14 s : written 64 KiB, CLI RSS 18.4-19.5 MB (flat)
t=15..25 s: ~12 MB/s (the reader's pace), RSS flat
reader: "got 134217728 <sha>" == host sha  -> no_drop true (6/6)
```

With 4 KiB writes the writer also stalled at exactly 64 KiB, so the path holds about one pipe buffer; the CLI does not buffer without limit and does not drop.

### S6 stderr: separate stream, to the host's stderr pipe; not logged by the platform

Program writes 3 lines to each stream and exits 3:

```text
default      stdout "OUT-0\nOUT-1\nOUT-2\n"   stderr "<mark>-err-0..2\nError: failed to exec in container: cmd.Wait(exec) failed: exit status 3\n\n"   rc 1
--stderr=false stdout same                      stderr "Error: failed to exec ... exit status 3"  (child's stderr discarded)
--stdout=false stdout ""                        stderr child lines + Error line           (child's stdout discarded)
```

- The CLI adds its own `Error: ...` line to **stderr** on any non-zero exit; the child's exit code is collapsed to CLI rc 1 (the real code is only in that text).
- A Cloud Logging search for the markers finds them only inside the logged command lines, never as output: with `--stdout=false`/`--stderr=false` the stream is dropped, not redirected to logs.
- The platform writes `[start] cwd=/ "<full argv>"` and `[end] exit_code=N elapsed=..." ` to log `run.googleapis.com//var/log/sandbox.log` for every `sandbox` invocation. This is the reason for the "no secret on a sandbox command line" rule; it is confirmed.

### S7 shutdown

| Case | Result (6 instances) |
| --- | --- |
| a. child writes `bye` and exits, host stdin still open | host EOF 19-24 ms after `bye`; CLI exits at the same moment. Nothing waits for the host to close stdin |
| b. host closes stdin | child sees EOF in 0.2-0.5 ms (wall clock inside vs outside); CLI exit 32 ms after close (x5 each) |
| c. child exits leaving a grandchild (`sleep 8`) holding stdout | EOF 23-26 ms after `bye`: the sandbox is torn down with its main process; grandchildren do not hold the pipe open |
| d. child closes its stdout but keeps running 6 s | **host sees EOF only at process exit (6.02 s)**, not at the close: no half-close on the sandbox-to-host direction |
| e. host SIGTERMs the `sandbox` CLI | CLI dies in 3-90 ms with rc -15, **but the sandboxed child keeps running** (a tick file on a bind mount kept growing: 20 -> 80 bytes in 3 s, 3/3) |
| f. same, with `--sandbox-name`, then `sandbox delete --force <name>` | still running after SIGTERM; `delete` rc 0 in 69 ms; ticks stop (`running_after_delete: false`) |

(f) took two attempts: in `b3-stdio-qkwvs` every stdout line of the job was lost (container exited ~4 s after the last print); adding a 15 s sleep before exit fixed it in `b3-stdio-8fsj8`.

### S8 output corruption: one stray write desynchronises a framed stream permanently

Child emits 20 frames `[u32 BE length][payload]` with one `print("WARNING: library chatter on stdout")` after frame 10:

```text
frames parsed 11, correct 11, then: absurd length 1463898702 (0x5741524e = "WARN") at offset 278 -> desynchronised
raw: b'10frame-10frame-10WARNING: library chatter on stdout\n\x00\x00\x00\x18frame-11...'
control (no chatter): 20 frames, clean
```

The CLI itself never writes to stdout: `sandbox do -- true` gives 0 bytes (5/5), and a failed exec (`/nonexistent/binary`) puts its error on stderr with 0 stdout bytes. So the only corruption source is the sandboxed process (and its libraries).

### Log loss (not the transport, but it bit the measurement)

Job-container stdout -> Cloud Logging dropped lines twice: two consecutive lines mid-run (xfphc task 2) and the entire output of a container that exited immediately after printing (qkwvs). Results should be written somewhere durable, or at least the process should linger before exit.

## Verdict

**Usable as a transport for TCP traffic**, with precautions. It is binary-clean, interactive (no batching), 200-500 MB/s, ~150 us median RTT, and applies real backpressure with ~64 KiB in flight and no loss. EOF propagates promptly in both directions on process exit and host close.

Precautions:

1. **Dedicate stdout to the protocol (S8).** Run the bridge endpoint as the sandbox's main process with its own stdout reserved; send all logging to stderr; ideally have the endpoint `dup` fd 1 away and re-point fd 1 at stderr before loading anything else. Use framing with a magic/checksum so corruption is detected and the connection is failed, not mis-parsed.
2. **Half-close does not work sandbox-to-host (S7d).** A TCP FIN from the sandbox side cannot be signalled by closing stdout; carry FIN/RST as in-band frames and treat process exit as the only real EOF. Host-to-sandbox close works (S7b), but carry it in-band too for symmetry.
3. **Killing the CLI does not kill the sandbox (S7e).** Always pass `--sandbox-name`, and on host shutdown or timeout run `sandbox delete --force <name>` (S7f, 69 ms). Otherwise orphans keep running and consume the instance's CPU/memory.
4. **Exit codes are collapsed** to 1 by the CLI; if the status matters, send it in-band or parse the CLI's stderr `Error:` line.
5. **Do not rely on the latency tail**: every 1000-message run had stalls of 13-29 ms; multiplexing many TCP streams over one pipe needs flow control per stream so one slow consumer does not stall the rest (the pipe itself only offers one global 64 KiB window).
6. **No secrets in the argv**: the platform logs the full command line to `/var/log/sandbox.log`.

## Resources and cost

Created: Cloud Run job `b3-stdio` (5 executions, 11 tasks total), service account `b3-job`. No images, repositories or buckets. Deleted both with `cleanup.sh` (run, verified empty). Logs remain in Cloud Logging under the default retention.

Approximate spend: about 800 task-seconds at 2 vCPU / 2 GiB, roughly US$0.03 (about £0.03) plus negligible logging.
