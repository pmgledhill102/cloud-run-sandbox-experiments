# Prompt 5: threat-model the runner's trust boundary

<!-- cspell:ignore jitconfig -->

Copy everything below the line into a fresh agent session. It needs no cloud
access: it is a reading and reasoning task, and the output is a document.
Attach or paste the design you want modelled where marked.

---

## Goal

Produce a threat model for a self-hosted GitHub Actions runner design in which
each GitHub job runs in its own Cloud Run job execution, and the runner and
all job steps run inside a Cloud Run sandbox with no network egress. Traffic
leaves only through a hostname-allowlisting proxy outside the sandbox, carried
over the sandbox process's standard input and output. A separate broker in the
host container submits Docker builds to Cloud Build on the job's behalf.

The design is below, between `<design>` tags. Model **that design**, not a
generic CI system.

<design>
PASTE THE ARCHITECTURE, IDENTITIES TABLE, EGRESS ALLOWLIST AND BUILD-BROKER
DESCRIPTION HERE. Use tags rather than a code fence: the design usually
contains fences of its own. Paste the whole design, or list the sections you
left out: a missing section (timeouts, the broker's build config, registry
layout) otherwise becomes a guess.
</design>

Context the design may not state, which you must fill in before running:

- **Account type:** `<personal account | organisation>`. Runners are
  registered per repository. Fork pull requests `<can | cannot>` run on them.
- **Evidence:** `<attached spike results | none>`. Where the design says
  something was tested and no results are attached, label it **claimed,
  evidence not reviewed**, not tested.

## Before you write

1. Read the current official documentation for: Cloud Run sandboxes and
   their isolation guarantees and launch stage; the Cloud Run container
   contract; GitHub's self-hosted runner security guidance and just-in-time
   runner API; GitHub's required runner hosts; Cloud Build's default pool
   networking. Record each URL with today's date.
2. Note where the design depends on a claim the documentation does not make.
   Those are the first findings.

## What to produce

`THREAT-MODEL.md`, with these sections:

1. **Assets.** What is worth stealing or abusing: cloud identities, the
   GitHub App key, just-in-time runner configs, `GITHUB_TOKEN`, caches,
   build outputs, source code, the bill.
2. **Trust boundaries**, drawn: internet ↔ webhook receiver, receiver ↔
   dispatcher, dispatcher ↔ GitHub, host container ↔ sandbox, sandbox ↔
   proxy ↔ internet, broker ↔ Cloud Build.
3. **Adversaries.** At least: a malicious workflow step in a trusted repo
   (compromised dependency); a malicious pull request author (state whether
   the design admits them at all); someone who can forge webhooks; someone
   with read access to the Cloud Run project, meaning any of `roles/viewer`,
   `roles/run.viewer` or log-viewing roles, modelled separately.
4. **Threats**, as a table: threat, boundary crossed, precondition, impact,
   existing control, residual risk, and whether a control is **tested**,
   **documented** or **assumed**. Cover at least:
   - exfiltration over allowlisted hosts (blob storage, `github.com`, package
     registries);
   - reaching the metadata server or the host container's identity;
   - the broker as a confused deputy (building or pushing something the job
     should not be able to);
   - just-in-time config exposure in execution arguments;
   - a leftover idle runner taking a later job;
   - cache poisoning across jobs or between hosted and self-hosted runners;
   - denial of wallet (a matrix of hundreds of jobs; a stuck execution);
   - a sandbox escape, and what the blast radius is if one happens. In scope:
     escape into the host container. Out of scope: escape from the Cloud
     Run instance into Google's infrastructure;
   - Cloud Build's default pool egress bypassing the proxy.
5. **What the allowlist does not do.** State plainly the difference between
   preventing direct egress and preventing exfiltration.
6. **Recommendations**, ranked by risk reduced per unit of effort, each
   marked as a change to the design, a test to add, or a documentation
   claim to verify.
7. **Tests that would prove the key controls**, each with a negative control
   that shows the test can fail. A negative control may use a deliberately
   weakened, test-only build of a component; say so where it does.

## Rules

- Do not call any control "secure" or a "boundary" without saying what
  evidence supports it.
- Do not invent product features. If you are unsure a Cloud Run or GitHub
  capability exists, say so and mark it **not verified**.
- Keep speculative mitigations (per-job workload identity, SPIFFE, VPC
  Service Controls) in a separate "future" section, and state their
  limitations accurately. VPC Service Controls protect Google APIs, not
  arbitrary internet hosts.

## Final report

End with the five highest residual risks in one table, and the three tests
you would run first.
