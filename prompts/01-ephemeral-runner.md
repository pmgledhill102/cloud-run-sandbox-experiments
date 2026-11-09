# Prompt 1: one ephemeral GitHub runner in a no-egress Cloud Run sandbox

<!-- cspell:ignore jitconfig gVisor setgroups setuid -->

Copy everything below the line into a fresh agent session. Fill in the three
values in the **Inputs** section first.

---

## Goal

Build and prove a GitHub Actions self-hosted runner that lives for exactly
one job, inside a **Cloud Run job execution**, with the runner and every job
step running inside a **Cloud Run sandbox that has no network egress**. The
only way out of the sandbox is an HTTP(S) proxy, running outside the sandbox
in the same instance, that allows a fixed list of hostnames and refuses
everything else.

This is an experiment, not a product. Optimise for evidence: every claim you
make at the end must point at a command output or a log line.

## Inputs

- **Google Cloud project:** `<PROJECT_ID>`. You have `roles/owner` on it. It
  has a spend cap; stay well inside it. Use region `europe-west1`.
- **GitHub repository:** `<OWNER>/<REPO>`, a throwaway test repository. You
  may push workflow files straight to its default branch (`workflow_dispatch`
  only works for a workflow that exists there). Don't force-push.
- **GitHub credential:** Secret Manager secret `<SECRET_NAME>` in that
  project holds a token with repository Administration write on that repo,
  and nothing else: **no Actions permission**, so it cannot cancel or
  force-cancel runs.
  Read it **only from inside Google Cloud** (the Cloud Run job's identity),
  never into this conversation, a log line, a file in the repo, or a
  command-line argument.

Your own session may not be able to call GitHub's self-hosted runner API at
all. That is expected: the runner registration must be minted from inside
Cloud Run, which is how a real dispatcher would do it.

## Before you build anything

1. Read the **current** official documentation for: Cloud Run jobs; Cloud
   Run sandboxes (code execution) for jobs, including whatever setting
   enables them and its launch stage; Cloud Run's container contract
   (shutdown and signals); GitHub's just-in-time runner configuration API;
   and GitHub's list of hosts a self-hosted runner must reach. Record each
   URL and today's date in `NOTES.md`.
2. Check whether your installed `gcloud` supports the sandbox setting. If it
   does not, use the Cloud Run Admin API directly. Record which you used and
   the gcloud version.
3. Write down, before testing, which of your assumptions the docs did not
   confirm. Test those first.
4. Read **Observed on 2026-11-07** below and re-check each fact. They held
   on that date, on a preview feature, and any of them may have changed.

## Observed on 2026-11-07: verify, don't trust

- gcloud 570.0.0 had no flag for the sandbox launcher. The Cloud Run Admin
  API accepted `sandboxLauncher: true` on the container, with launch stage
  `BETA`.
- The platform logs the **full command line** of every `sandbox do`
  invocation, `--env` values included.
- Processes in the sandbox ran as uid 0, but could not `chown`, `setuid` or
  `setgroups`. So `sudo` and `apt-get` failed at job time, and files owned by
  another uid in the image weren't writable.
- Files the host wrote outside `/tmp` were readable from inside the
  sandbox, whatever their mode.
- Images had to be built with Cloud Build: the agent session could not reach
  a container registry.

## Constraints

- One Cloud Run **job** execution per GitHub job. No worker pools, no
  services, no VMs, no Kubernetes.
- The runner is registered with a **just-in-time configuration** (single
  use, ephemeral), minted inside the execution, outside the sandbox.
- **Use the runner label `prompt-test-1`** and no other self-hosted label.
  Other infrastructure may serve other labels on this repository; do not
  disturb it.
- Inside the sandbox: no network egress, and no access to the instance's
  metadata server. Do not enable sandbox egress except in the negative
  control below.
- The proxy filters on hostname. No TLS interception and no extra CA.
  Refuse IP literals. Refuse a hostname if any address it resolves to is
  private, link-local or loopback.
- **No secret on a command line or in a host file the sandbox can read.**
  That includes the just-in-time config: pass it over the bridge, or another
  channel the platform does not log.
- Pin every version you install (base image, runner, tools). Record them.
- No Docker daemon is available on Cloud Run. Do not try to provide one.
- Keep the Cloud Run job's task timeout at or below 60 minutes, and give
  every workflow job a `timeout-minutes` below that.

## The hard part, stated so you do not lose a day to it

Something has to carry TCP from inside the sandbox to the proxy outside it.
Find out empirically which channels actually cross the sandbox boundary
before designing around one. Candidates include files or sockets on a shared
mount, a FIFO, loopback, and the sandboxed process's own standard input and
output. Probe each one, record the result in `NOTES.md` as a table, and build
on what works. If a channel needs to carry more than one connection,
multiplex it.

## Acceptance tests

Each test must be run, and its output kept. Each negative control must be
run too: it proves the test can fail.

| # | Test | Pass if | Negative control |
| --- | --- | --- | --- |
| A1 | A `workflow_dispatch` job on `prompt-test-1` runs to success | Job green; the runner deregisters itself and the execution ends on its own | — |
| A2 | From a job step, `curl` an allowed host (`https://api.github.com/zen`) | Succeeds through the proxy | — |
| A3 | From a job step, `curl` a host not on the list (`https://example.com`) | Refused by the proxy | Add the host to the list: it succeeds |
| A4 | From a job step, connect directly to an internet host, **ignoring the proxy** | Fails (no route or no DNS) | Same test from the host container, outside the sandbox: it succeeds |
| A5 | From a job step, reach the metadata server by IP and by name, directly and through the proxy | All four fail | Same test from the host container: direct access succeeds |
| A6 | Enable sandbox egress, rerun A4 | A4's direct connection now **succeeds** | This *is* the control for A4. Enable it per execution, or restore no-egress afterwards |
| A7 | Start-up time from `jobs.run` to the GitHub job starting, over at least 10 runs | Report p50 and p90, and the time spent in each stage you can see. Use `fail-fast: false` on any matrix, so one failure doesn't cancel the samples | — |
| A8 | A runner started for a job that is cancelled before pickup | It exits by itself within a bounded idle time you choose, without taking another job forever, and deregisters | Optional: cancel during the runner's listening window as well |

A test the job cannot see (A4 and A5's host-container controls) may be run
from the launcher, with its output in the execution's logs.

## Expected artefacts

- `NOTES.md`: docs read (URLs, date), versions, the boundary-channel table,
  each acceptance test with its result and the evidence, timings, and every
  difference you found between this runner and a GitHub-hosted one.
- The image source (`Dockerfile` and whatever runs in it), the proxy, the
  bridge, the job definition, and a script that recreates everything from an
  empty project.
- The workflow file(s) used for the tests.
- A clean-up script that removes everything you created, and proof you ran
  it, except anything the owner asks you to keep.

## Known failure cases to expect, not avoid

You may hit some of these. If you do, record the cause and the fix:

- Tools that ignore `HTTPS_PROXY` unless told to.
- The sandbox running as a different user from the one GitHub's tooling
  expects, and file ownership changes failing.
- `apt-get install` failing at job time.
- A runner that is killed mid-job leaving the job "in progress" on GitHub
  for minutes. Do not spend more than an hour trying to fix this; record it.

## Cost and clean-up

- Budget: under £5 of spend for the whole exercise. You probably have no
  billing access: estimate it from execution-seconds in the executions list,
  times the current list price, and say that it is an estimate. Time spent
  paused by the environment does not count against any time limit.
- Delete the Cloud Run job, images and any registry you created when done,
  unless the owner asks otherwise. Remove any self-hosted runners still
  registered on the repo.

## Final report

End with a table: each acceptance test, pass or fail, and the evidence. Then
list what you assumed that you could not verify, and what you would change in
this prompt so the next agent gets further faster.
