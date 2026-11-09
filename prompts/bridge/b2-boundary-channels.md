# Prompt B2: which channels cross the host/sandbox boundary

<!-- cspell:ignore proc FIFOs rootfs -->

Copy everything below the line into a fresh agent session. Fill in **Inputs**
first.

---

## Goal

A Cloud Run sandbox with default settings has no network. A trusted process
in the host container needs to exchange bytes with an untrusted process in
the sandbox. Find out, by probing, **which channels actually carry bytes
across the boundary, in which direction**. Don't design around any channel
until it has been probed.

## Inputs

- **Google Cloud project:** `<PROJECT_ID>`, region `<REGION>`. You have
  `roles/owner`. A spend cap applies.

## Before you build

1. Read the current Cloud Run sandbox documentation and `sandbox` CLI
   reference, especially `--mount`, `--rootfs`, `--stdin`/`--stdout`, and
   anything said about filesystems. Record URLs and the date.
2. Note what the docs promise, and what they don't say.

## Observed on 2026-11-07: verify, don't trust

- gcloud 570.0.0 had no flag for the sandbox launcher, and `gcloud run jobs
  replace` rejected the field. The Cloud Run Admin API v2 accepted
  `sandboxLauncher: true` on the container, with launch stage `BETA`.
- The platform logs every `sandbox` command line, `--env` values included.
- Processes in the sandbox ran as uid 0, despite the docs' "non-root user".
- Cloud Build's default staging bucket is in `us`, which an EU-only location
  policy refuses: stage source in your own regional bucket, with your own
  build identity.

## Channels to probe

Probe each **in both directions** (host → sandbox and sandbox → host) where
that makes sense, and record the exact error when one fails.

| # | Channel |
| --- | --- |
| H1 | A Unix-domain socket the host listens on, in a bind-mounted directory |
| H2 | A Unix-domain socket the sandbox listens on, in that directory |
| H3 | A FIFO in that directory, each direction |
| H4 | TCP on loopback: host listens, sandbox connects; the reverse; and the sandbox connecting to the host's own address. Say whose loopback each test used |
| H5 | A regular file in that directory: written by one side, read by the other. Test a new file written **after** the sandbox starts, an existing file rewritten after start, and a sandbox write read by the host while the sandbox is still running |
| H6 | The sandboxed process's own **stdin and stdout**, as the host's child process |
| H7 | The host container's own filesystem outside any mount: a file the host writes to `/etc`, `/root` and `/tmp` before starting the sandbox, and one written after. Readable inside? |
| H8 | Anything else the documentation suggests. Say what you tried |

Also record what `/proc/mounts` says about the bind mount inside the sandbox.

## Acceptance

| # | Pass if |
| --- | --- |
| P1 | Every channel H1–H7 has a result per direction, with the exact error or the bytes seen |
| P2 | Each **working** channel is shown working with a value that couldn't be there by accident (a random token written by one side and read by the other) |
| P3 | Each **failing** channel has a control showing the same probe works **outside** the sandbox (host to host), so the failure belongs to the boundary, not the probe |
| P4 | At least three executions **of the same probe version**, and a statement of whether every result repeated |

## Rules

- H7 is a security question, not a transport one. Report what is visible,
  whatever its file mode, and say what that means for secrets on the host.
- No secret on any `sandbox` command line: the platform logs them, `--env`
  values included. `--env` is a channel, but a logged one.
- Stop after 90 minutes or about £2 of spend, and after three failed
  attempts at any one probe.

## Artefacts

`NOTES.md` with a channel × direction table and the evidence, the probe
scripts, the job definition, and a clean-up script you have run.

## Final report

The table, a recommendation of which channel to build a transport on and
why, and what you'd change in this prompt.
