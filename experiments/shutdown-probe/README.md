# Shutdown probe: when does `sandbox do` exit?

<!-- cspell:ignore hsmtv crun -->

Settles a conflict between two cold runs. B4 saw `sandbox do` wait 15 s for
its stdin to close after a 79 ms command, and read that as the CLI waiting
for stdin EOF. B3 saw the CLI exit within 24 ms of its command with stdin
still open. The full write-up had given B4's reading as the root cause of
the bridge's 39-second shutdown wait.

## What it does

`probe.py` runs on the host of a Cloud Run job with sandboxes enabled. For
each case, three times, it starts `sandbox do` with its streams piped, waits
for the command's `done` line, and measures when stdout reaches EOF and when
the CLI exits, **while holding stdin open**. If the CLI hasn't exited 20 s
later, it closes stdin and measures again.

| Case | What differs |
| --- | --- |
| `sh-plain` | `/bin/sh -c 'echo done'`, no flags |
| `py-plain` | Python, no flags |
| `py-stdin-true` | `--stdin=true` |
| `py-write` | `--write` |
| `py-write-mount` | `--write` and a bind mount |
| `py-rig-flags` | The sandbox-bridge rig's exact flags |
| `py-unread-stdin` | 64 KiB written to stdin that the command never reads |
| `py-reads-a-line` | The command reads one line of stdin first |
| `py-stdin-false` | `--stdin=false` |

## Result, 2026-11-08, execution `h7hb6`

**27 of 27 runs: the CLI exited about 32 ms after its command (range
32–64 ms), with stdin still open.** It never waited for stdin, in any case.
stdout reached EOF 16–33 ms after `done`. Raw results:
`results/2026-11-08-h7hb6.jsonl`.

So the waits in B4 and in the rig's early bridge came from what ran inside
the sandbox, most likely a bridge's inside end reading stdin until EOF, not
from the CLI. *[Inferred: the bridges themselves weren't instrumented.]*

**First run, `hsmtv`:** the Python cases failed with exit code 1 because a
sandbox inherits no `PATH`, so a bare `python3` isn't found. `/bin/sh` and
the rig-flags case, which passes `PATH`, worked and gave the same 32 ms.
Fixed by using `/usr/bin/python3`; kept as
`results/2026-11-08-hsmtv-bare-python3.jsonl`. Two of 27 result lines were
lost in that run's logs, as B3 and B6 also saw.

## Run it

```sh
PROJECT=<id> IMAGE=<any image with /usr/bin/python3> python3 run.py   # needs PyYAML locally
```

`run.py` inlines `probe.py` into the job's environment, creates or updates
the job through the Admin API (`../sandbox-agent/crun.py`, because gcloud
rejects `sandboxLauncher`), and starts one execution. It used the
sandbox-agent POC's image. The job identity needs only to write logs.
