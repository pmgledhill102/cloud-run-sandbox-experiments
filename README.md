# An egress allowlist for Cloud Run sandboxes

<!-- cspell:ignore yamux -->

Cloud Run sandboxes, in preview, give untrusted code no network at all, or,
with `--allow-egress`, everything the host can reach. This is the evidence,
code and write-up for a way in between: an allowlist of hosts the code can't
bypass, carried over the sandboxed process's own stdin and stdout to a proxy
outside it. It was tested with two workloads, GitHub Actions jobs and coding
agents (Claude Code and Gemini CLI), the agents reaching their models through
a host-side gateway so they never hold a credential.

**An experiment on a preview feature, tested in November 2026.** It's a
reference to learn from, not a product or a finished security boundary.

| Path | What it is |
| --- | --- |
| [`article.md`](article.md) | The full write-up: every claim, how it was tested, its limits |
| [`medium.md`](medium.md) | The short version, about a five-minute read |
| [`img/`](img/) | Figures, with their HTML sources. `node img/render.js` re-renders them |
| [`experiments/sandbox-bridge/`](experiments/sandbox-bridge/) | The rig behind the egress and channel tables: proxy, bridge, probes, raw results |
| [`experiments/sandbox-agent/`](experiments/sandbox-agent/) | The coding-agent POC: model gateway, compromised-dependency test, raw results |
| [`experiments/shutdown-probe/`](experiments/shutdown-probe/) | When `sandbox do` exits, measured across nine flag and stdin cases |
| [`prompts/`](prompts/) | Prompts that let a coding agent rebuild and test each part on current Cloud Run, and the results of cold runs |

## Reproducing

Each experiment's README says what it needs and how to run it. Both run as
single Cloud Run job executions in a throwaway project and cost pennies of
compute. The agent POC also uses a small number of model requests.

The prompts are the other way in. Each states a goal, the threat boundary,
acceptance tests with negative controls, a cost limit and clean-up, and asks
the agent to read the current documentation first. A prompt counts as
tested only once an agent given nothing but the prompt passes every test.

## Status

Tested on a preview feature in November 2026. Every result carries its date;
sandbox behaviour may have changed since.

## Licence

[MIT](LICENSE), for everything here: code, prose, figures and prompts.
