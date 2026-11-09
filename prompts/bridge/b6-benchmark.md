# Prompt B6: benchmark the ways out of a Cloud Run sandbox

Copy everything below the line into a fresh agent session. Fill in **Inputs**
first.

---

## Goal

Measure what the stdio tunnel costs in throughput, latency and start-up
against the alternatives, so a reader can tell whether it's fast enough for
their use. Measure the **transport**, not the internet: use a server you
control in the same region.

## Inputs

- **Google Cloud project:** `<PROJECT_ID>`, region `<REGION>`. You have
  `roles/owner`. A spend cap applies.
- **The tunnel:** `<WHERE IT IS>`. Prefer a supplied one: a tunnel you
  build makes R3 partly a measure of your own code. If you build one, say
  so, and decompose R3 (see M6).

## Before you measure

1. Read the current Cloud Run sandbox docs and `sandbox` CLI reference.
   Record URLs and the date.
2. The host is a program in a **Cloud Run job** with sandboxes enabled. "The
   proxy" is an allowlisting `CONNECT` proxy on the host, with TLS end to end,
   so the tunnel and proxy carry only ciphertext.
3. Stand up a test server in the same region that serves fixed-size payloads
   and echoes small requests, reachable **privately**: for example a small VM
   with an internal address, reached over the job's Direct VPC. Never make it
   public and unauthenticated. Record its location.

## Observed on 2026-11-08: verify, don't trust

- gcloud 570.0.0 had no flag for sandboxes and rejected `sandboxLauncher` in
  a job YAML. The Cloud Run Admin API accepts it.
- After a Direct VPC job is deleted, Cloud Run holds an address in its subnet
  for 1–2 hours, so the subnet and network can't be deleted until then.
- Cloud Logging lost about 5% of result lines. Write results durably, or
  repeat enough to tolerate gaps.

## Paths to compare

| # | Path |
| --- | --- |
| R1 | Host container, direct (baseline) |
| R2 | Host container, through the proxy only (no sandbox) |
| R3 | Sandbox, through the stdio tunnel and proxy (the design) |
| R4 | Sandbox with `--allow-egress`, direct (what isolation costs nothing for) |
| R5 | A regular file on a bind mount, as a crude transport, if it works |

## Measurements

| # | Measure |
| --- | --- |
| M1 | Single-stream throughput, 10 and 100 MiB |
| M2 | Aggregate throughput with 1, 4 and 16 parallel streams, 16 MiB each |
| M3 | Request latency, p50 and p90, over at least 500 small requests on one warm connection |
| M4 | Time from `sandbox do` to the first successful request through the tunnel; for paths with no sandbox, from process start |
| M5 | CPU during M1: host-side tunnel and proxy processes, the `sandbox do` process, and the container total. Say where in-sandbox figures come from |
| M6 | Decomposition: raw `sandbox do` stdin and stdout with no framing, and the client against a host-loopback server, as ceilings |

## Acceptance

| # | Pass if |
| --- | --- |
| P1 | M1–M4 for R1–R4 (and R5 if it works), each from at least three separate job executions, with the spread |
| P2 | The test server is in the same region, so R1's numbers are a ceiling, not an internet measurement |
| P3 | A short statement of where the tunnel's cost comes from (CPU, the proxy, a cap), with the evidence that points there |

## Rules

- R4 is the only place `--allow-egress` is used.
- No secret on any `sandbox` command line.
- Stop after 2 hours or about £5 of spend, and after three failed attempts
  at any one measurement. Delete the test server when done.

## Artefacts

`NOTES.md` with the tables and evidence, the scripts, and a clean-up script
you've run.

## Final report

A table of paths × measures, the verdict, and what you'd change in this
prompt.
