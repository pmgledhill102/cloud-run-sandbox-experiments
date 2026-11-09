# Prompts for the article: networking a sandbox over stdin and stdout

Seven prompts that let a coding agent rebuild and test the technique in
`../../article.md`, on whatever Cloud Run version is
current. The method is the same as for
the runner prompts (`../README.md`): a prompt counts as tested only once a
cold agent, given nothing but the prompt, passes every acceptance test,
negative controls included.

| # | Prompt | Builds on | Status |
| --- | --- | --- | --- |
| B1 | [Isolation and the `--allow-egress` control](b1-isolation-control.md) | — | Run 1 passed; revised |
| B2 | [Which channels cross the boundary](b2-boundary-channels.md) | — | Run 1 passed; revised |
| B3 | [Is stdio a usable transport](b3-stdio-transport.md) | — | Run 1 passed; revised |
| B4 | [Build the multiplexed proxy tunnel](b4-multiplexed-tunnel.md) | B2, B3 findings | Run 1 passed; revised |
| B5 | [Adversarial validation](b5-adversarial.md) | A working tunnel (B4's or yours) | Run 1 passed, against the rig's tunnel; revised |
| B6 | [Benchmark the transports](b6-benchmark.md) | B4 | Run 1 passed, with its own tunnel; revised |
| B7 | [A coding agent in the sandbox, with a host-side model gateway](b7-agent-gateway.md) | A working tunnel (B4's or yours) | Run 1 passed, Gemini only; revised |

B1–B3 each stand alone and are cheap: one Cloud Run job, a few executions.
B4 is the build. B5, B6 and B7 need a working tunnel and say so in their inputs.

## Shared rules, repeated in each prompt

Each prompt states these itself, so it can be handed over alone:

- Read the **current** Cloud Run sandbox documentation and CLI reference
  first, and record URLs and the date.
- **Never leave `--allow-egress` on** except where a test names it as the
  negative control. Default egress is what is under test.
- **No secret on any command line** passed to `sandbox`. The platform logs
  those command lines.
- Every claim in the final report points at a log line or command output,
  and is labelled tested, documented or assumed.
- Stop conditions: a spend limit, a time limit, and a three-attempt limit on
  any single failure before reporting it instead of guessing further.
