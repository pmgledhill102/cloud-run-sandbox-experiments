# Prompts for the Cloud Run runner experiment

Prompts that let a coding agent rebuild the GitHub Actions runner, the worked
example in the sandbox article (`../article.md`), on whatever
Cloud Run, gcloud and GitHub versions are current when it is run.

**A prompt is not a substitute for the reference.** Each one is tested by
giving it, and nothing else, to a fresh agent session with an empty sandbox
project. What that agent builds is then scored against the prompt's own
acceptance tests. The known-good implementation is kept for comparison and
is not something the prompt may point the agent at.

| # | Prompt | Status |
| --- | --- | --- |
| 1 | [One ephemeral runner in a no-egress Cloud Run sandbox](01-ephemeral-runner.md) | Run 1 passed A1–A8; revised after it |
| 5 | [Threat model of the trust boundary](05-threat-model.md) | Run 1 done; revised after it |

The bridge prompts (B1–B7) are in [`bridge/`](bridge/README.md).

## How a run is recorded

Each cold run gets a section in `runs.md`: date, model, tool versions,
which acceptance tests passed, where the agent guessed or stalled, cost, and
the prompt change it led to. A prompt is only marked "tested" after a cold
run passes every acceptance test, including the negative controls.

## Conventions every prompt follows

- States the goal, constraints, threat boundary, expected artefacts,
  acceptance tests with negative controls, known failure cases, a cost limit
  and clean-up.
- Requires the agent to read **current** official documentation before using
  any preview feature, and to record the doc URLs and the date it read them.
- Requires pinned versions in what it builds and a record of every version
  it used.
- Never asks for a credential in the conversation. Credentials are named by
  where they live (a Secret Manager secret), and the agent reads them from
  inside Google Cloud.
