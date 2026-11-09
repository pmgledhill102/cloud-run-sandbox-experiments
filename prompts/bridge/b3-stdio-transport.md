# Prompt B3: is the sandbox's stdio a usable transport?

<!-- cspell:ignore Backpressure -->

Copy everything below the line into a fresh agent session. Fill in **Inputs**
first.

---

## Goal

A Cloud Run sandbox can be started as a child process of a trusted host
process, which then owns its stdin and stdout. Decide, with measurements,
whether that pipe pair is good enough to carry TCP traffic: **binary-clean,
interactive, fast enough, and well behaved at the edges**.

## Inputs

- **Google Cloud project:** `<PROJECT_ID>`, region `<REGION>`. You have
  `roles/owner`. A spend cap applies.

## Before you build

Read the current Cloud Run sandbox documentation and `sandbox` CLI reference,
especially stdin, stdout and stderr forwarding. Record URLs and the date.

The `sandbox` CLI exists only inside a container with sandboxes enabled, so
the host is a program running in a **Cloud Run job** with sandboxes enabled,
which starts `sandbox do` as its child with its streams piped.

## Observed on 2026-11-08: verify, don't trust

- gcloud 570.0.0 had no flag for sandboxes and rejected `sandboxLauncher` in
  a job YAML. The Cloud Run Admin API accepts it.
- Cloud Logging lost job stdout: lines mid-run, and all of a container's
  output when it exited straight after printing. Write results somewhere
  durable, or pause before the container exits.

## Measurements

| # | Question | How |
| --- | --- | --- |
| S1 | Binary-clean? | Send at least 1 MiB of random bytes in, echo them back, compare SHA-256 |
| S2 | Interactive? | Write one line, wait for its reply **before** closing stdin; then a second line after a pause. The second reply must arrive after the pause, not at exit |
| S3 | Throughput | One way and round trip, for 1, 10 and 100 MiB. Report MB/s |
| S4 | Latency | Round-trip time for small messages, p50 and p90 over at least 1,000 |
| S5 | Backpressure | Write faster than the sandbox reads. Does the writer block, buffer without limit, or drop data? |
| S6 | stderr | Is stderr separate from stdout, and where does it go? |
| S7 | Shutdown | When the sandboxed process exits, how soon does the host see EOF, and how soon does the `sandbox` CLI itself exit, with the host's stdin still open? When the host closes stdin, how soon does the process see it? Does closing stdout inside, with the process still running, reach the host (half-close)? If the host kills the `sandbox` CLI, does the sandboxed process stop, and if not, what stops it? |
| S8 | Output corruption | If the sandboxed process writes anything else to stdout, what happens to a framed protocol running over it? |

## Acceptance

| # | Pass if |
| --- | --- |
| P1 | S1–S8 each have a measured answer with the command and output |
| P2 | S1's control: corrupt one byte deliberately and show the hashes then differ |
| P3 | S3 and S4 come from at least three separate instances, with the spread, not one best number, plus a baseline over a plain pipe with no sandbox |
| P4 | A clear verdict: usable as a transport, with which precautions (S7 and S8 especially) |

## Rules

- No secret on any `sandbox` command line: the platform logs them.
- Stop after 90 minutes or about £2 of spend, and after three failed attempts
  at any one measurement.

## Artefacts

`NOTES.md` with each measurement and its evidence, the scripts, the job
definition, and a clean-up script you have run.

## Final report

The table, the verdict and precautions, and what you'd change in this prompt.
