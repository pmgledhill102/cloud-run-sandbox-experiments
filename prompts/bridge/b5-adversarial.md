# Prompt B5: adversarial validation of a sandbox egress tunnel

<!-- cspell:ignore CGNAT unbracketed rootfs sslip -->

Copy everything below the line into a fresh agent session. Fill in **Inputs**
first.

---

## Goal

You are given a working design: untrusted code runs in a Cloud Run sandbox
with no network, and reaches an allowlist of hostnames through a proxy
outside the sandbox, over a tunnel on the sandbox's stdin and stdout. **Try
to break it.** Find every way the untrusted code can reach something it
shouldn't, or get something it shouldn't have. Prove each attempt could have
succeeded, so that a failure means something.

## Inputs

- **Google Cloud project:** `<PROJECT_ID>`, region `<REGION>`. You have
  `roles/owner`. A spend cap applies.
- **The tunnel under test:** `<WHERE THE PROXY, BRIDGE AND JOB DEFINITION
  ARE>`. If none is supplied, build a minimal one first and say so: a proxy
  on the host, and a stdio tunnel to a listener inside.
- **The allowlist in use:** `<HOSTS>`.

## Before you attack

1. Read the current Cloud Run sandbox docs and `sandbox` CLI reference.
   Record URLs and the date.
2. Read the tunnel's code. List what it claims to enforce and where.
   Comments and code can disagree: note every place they do.

## Observed on 2026-11-07: verify, don't trust

- Cloud Build's default staging bucket is in `us`, which an EU-only location
  policy refuses, and a regional build runs as the default compute identity.
  Stage source and logs in your own regional bucket, with resource-level
  grants only.
- Public DNS test services (`nip.io`, `sslip.io`) may not resolve from your
  session, and give one address per name anyway. For deterministic answers,
  add `/etc/hosts` entries in the **host** container: a Go resolver reads
  that file first.
- Overrides passed to `jobs.run` are recorded in the System Event audit log.

## Attacks

| # | Attack | Control that shows it can succeed |
| --- | --- | --- |
| X1 | Direct egress ignoring the proxy: by name, IPv4 literal, IPv6 literal. Use a raw socket or a tool that ignores `HTTP(S)_PROXY`: curl honours them and turns a "direct" test into a proxy test | Same from a sandbox with `--allow-egress` |
| X2 | Metadata server: by IP and name, direct and through the proxy | Same from the host container |
| X3 | Proxy rules: IP literals, other ports, the unbracketed and bracketed IPv6 forms, a trailing dot, upper case, `@` and other URL tricks in the host | A permitted request in the same form |
| X4 | **DNS:** an allowlisted name resolving only to a private address; one resolving to **both** a private and a public address; one resolving to the metadata address | A public-only name, allowed |
| X5 | **Rebinding:** an answer that changes between the proxy's check and its dial. Can the proxy be made to dial something it didn't check? | Explain why your test could catch it if it existed |
| X6 | **Fronting:** `CONNECT` to an allowlisted host, then TLS naming a different server | A matching name, for comparison |
| X7 | **Host files:** files the host writes before starting the sandbox, in several paths and modes. Readable inside? Does `--rootfs` change that? | Read them from the host |
| X8 | **Logged values:** does the platform log `sandbox` arguments and `--env` values? Do `jobs.run` overrides reach any audit log? Where, for how long, and who can read them? Include the target's own deploy tooling | A known harmless value, found in the logs |
| X9 | **The tunnel itself:** can the inside end ask the host end for a service it wasn't configured with? Malformed frames, huge frames, many streams | A configured service, for comparison |
| X10 | Resource exhaustion: many connections, large transfers, a stream left open. Does anything hang or grow without bound? | — |

## Acceptance

| # | Pass if |
| --- | --- |
| P1 | Every attack X1–X10 has a result, its evidence, and its control's result |
| P2 | Every finding is labelled: **escape** (left the sandbox), **bypass** (reached something past the policy), **exposure** (something secret became readable), or **limitation** (a known gap by design, such as fronting) |
| P3 | Code-versus-comment disagreements are listed, with which one the observed behaviour matches |
| P4 | Each control ran, so every "blocked" result can be trusted |

## Rules

- You may extend the target's launcher to produce controls (for example a
  switch that adds `--allow-egress`), as long as the proxy, the bridge and
  the sandbox flags under test stay unchanged. Say what you changed.
- If the target's own setup script changes project-level IAM, use
  resource-level grants instead and say so.
- Attack only the project and tunnel named in Inputs. No third-party
  services beyond fetching public pages and public DNS-based test names.
- No secret on any `sandbox` command line. For X8, use a harmless marker.
- Stop after 3 hours or about £5 of spend, and after three failed attempts
  at any one attack.

## Artefacts

`FINDINGS.md`: each attack, its evidence, its control, its label, and a
severity with your reasoning. Plus the scripts and a clean-up script you've
run.

## Final report

The five most serious findings, and the three changes that would close the
most risk for the least work. Then what you'd change in this prompt.
