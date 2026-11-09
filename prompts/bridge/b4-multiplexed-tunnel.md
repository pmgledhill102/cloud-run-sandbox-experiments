# Prompt B4: build a multiplexed proxy tunnel over the sandbox's stdio

<!-- cspell:ignore CGNAT -->

Copy everything below the line into a fresh agent session. Fill in **Inputs**
first.

---

## Goal

Build the thing: untrusted code in a Cloud Run sandbox with **no network**
reaches an **allowlist of hostnames**, and nothing else, through a proxy that
runs **outside** the sandbox. The transport between them is the sandboxed
process's own stdin and stdout, multiplexed so that many TCP connections can
be open at once.

```text
host container (trusted)            sandbox (untrusted, no network)
  proxy: allowlist + checks           listener on 127.0.0.1:<port>
  bridge, host end  <== stdin/stdout ==>  bridge, inside end
                                      untrusted code, HTTPS_PROXY=127.0.0.1:<port>
```

## Inputs

- **Google Cloud project:** `<PROJECT_ID>`, region `<REGION>`. You have
  `roles/owner`. A spend cap applies.
- **Allowlist:** `<HOSTS>`, for example `api.github.com` and one other
  public host. `example.com` must **not** be on it.

## Before you build

1. Read the current Cloud Run sandbox documentation and `sandbox` CLI
   reference. Record URLs and the date.
2. Confirm for yourself, with a quick probe, that stdin and stdout cross the
   boundary binary-clean. Don't take this prompt's word for it.

## Observed on 2026-11-07: verify, don't trust

- gcloud 570.0.0 couldn't set the sandbox launcher: no flag, and `jobs
  replace` rejected the field. The Cloud Run Admin API accepted
  `sandboxLauncher: true`.
- **Shutdown can hang on the tunnel's own end.** `sandbox do` itself exits
  about 32 ms after its command, with stdin still open (shutdown probe,
  2026-11-08, 27 of 27). But a tunnel whose inside end keeps reading stdin
  until EOF won't let its command finish, so one side has to close first.
  An earlier run read this as the CLI waiting for stdin; it wasn't.
- Output the sandbox forwards on its **stderr** was lost at teardown. Don't
  rely on it for anything you need to read back.
- A build identity needed `storage.objectAdmin` **and**
  `storage.legacyBucketReader` on its own staging bucket. The default
  staging bucket is in `us`, which an EU-only location policy refuses.

## Requirements

**The proxy**

- `CONNECT` for HTTPS (port 443) and absolute-form requests for plain HTTP
  (port 80).
- Allow a request only if the hostname is on the allowlist, the port is 443
  or 80, and the target is a name, not an IP literal.
- Resolve the name, refuse it if **any** address is loopback, private,
  link-local, CGNAT, multicast or unspecified, then dial **the address you
  checked**, not the name again.
- One structured log line per decision: host, port, allow or deny, reason.

**The bridge**

- The host is a Cloud Run **job**. Its container runs the proxy and the
  host end, which starts the sandbox as its own child process and owns its
  stdin and stdout.
- Multiplex many TCP streams over that one pipe pair. A library is fine;
  say which and why.
- The inside end listens on loopback and opens one stream per accepted
  connection. Each stream names which host-side service it wants; the host
  connects only to names it was configured with.
- Inside, nothing but the tunnel may write to stdout. Send the wrapped
  command's stdout somewhere else.
- **Shutdown:** when the wrapped command exits, everything exits promptly.
  Measure it. A tunnel that waits on the other side to close first will
  hang, and that time is billed.

**Security**

- **No secret on any `sandbox` command line**, `--env` included: the
  platform logs them.
- Nothing secret written to the host's own filesystem outside `/tmp` before
  the sandbox starts: the sandbox can read it.
- The sandbox never gets `--allow-egress`, except in acceptance test T3's
  control.

## Acceptance tests

| # | Test | Pass if | Negative control |
| --- | --- | --- | --- |
| T1 | From inside, curl and one non-curl client fetch an allowlisted host through the proxy | 200 | — |
| T2 | Same for `example.com` | 403 from the proxy | Add it to the list: 200 |
| T3 | From inside, connect directly, ignoring the proxy | Fails | The same in a sandbox with `--allow-egress`: succeeds |
| T4 | From inside, the metadata server, direct and through the proxy | All fail | From the host: direct succeeds |
| T5 | 20 concurrent downloads through the tunnel | All complete, and each checksum matches a direct fetch of the same URL from the host | — |
| T6 | The wrapped command exits | Host and bridge exit within 2 s | Report what happens without your shutdown handling |
| T7 | The proxy's own rules: IP literal, port 22, an allowlisted name that resolves to a loopback address. This test may use a separate proxy instance with that name added to its list | All refused | — |

## Rules

- Stop after 3 hours or about £5 of spend, and after three failed attempts
  at any one problem: report it instead.
- Pin versions and record them.

## Artefacts

`NOTES.md` (docs, versions, design, every test with evidence, timings), the
proxy, the bridge, the image, the job definition, a recreate script, and a
clean-up script you have run.

## Final report

The test table with evidence, the design in a paragraph, what you assumed,
and what you'd change in this prompt.
