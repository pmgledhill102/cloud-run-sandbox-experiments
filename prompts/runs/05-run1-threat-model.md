# Threat model: GitHub Actions runners in Cloud Run job executions with sandboxed steps

Scope: the design in "5. Chosen architecture", "6. Egress control" and
"8. Security model" — receiver, dispatcher, one Cloud Run job execution per
GitHub job, host container (launcher, egress proxy, build broker), Cloud Run
sandbox (runner and all steps), `stdio-bridge`, and the Cloud Build shim.
This document does not model a generic CI system.

Prepared 2026-11-07. Evidence labels used throughout:

- **tested**: the design says the spike measured it. I have not seen the
  spike's evidence, so read "tested" as "tested, according to the design".
- **documented**: a current vendor page says it. The source is listed below.
- **assumed**: neither of the above. Several of the design's key claims fall
  here.
- **not verified**: I could not confirm that the capability or behaviour
  exists.

## 0. Sources read (all on 2026-11-07)

| # | URL | Used for |
| --- | --- | --- |
| S1 | https://docs.cloud.google.com/run/docs/code-execution | Sandbox isolation claims, Preview stage, rootfs, mounts, `--allow-egress`, tar warnings |
| S2 | https://docs.cloud.google.com/run/docs/reference/sandbox-cli | Flag semantics: `--rootfs` "exposes readable host container files", `--mount`, `--stdin` |
| S3 | https://docs.cloud.google.com/run/docs/configuring/jobs/sandboxes | Enabling sandboxes on jobs (`gcloud beta`, `sandboxLauncher`), shared CPU/memory |
| S4 | https://docs.cloud.google.com/run/docs/container-contract | Namespaces, no added capabilities, in-memory filesystem, metadata server, jobs on gen2 |
| S5 | https://docs.cloud.google.com/run/docs/execute/jobs | Override args/env/task count/timeout per execution |
| S6 | https://docs.cloud.google.com/iam/docs/roles-permissions/run and https://docs.cloud.google.com/run/docs/reference/iam/roles | Who holds `run.executions.get`; `run.jobs.sshRead`/`sshRoot` |
| S7 | https://docs.cloud.google.com/run/docs/configuring/max-retries | Default task retries = 3 |
| S8 | https://docs.cloud.google.com/run/docs/configuring/jobs/ephemeral-disk | Ephemeral disk semantics and regional quota |
| S9 | https://docs.cloud.google.com/build/docs/private-pools/private-pools-overview | Default pool has public internet; private-pool egress options |
| S10 | https://docs.cloud.google.com/build/docs/private-pools/use-in-private-network | `NO_PUBLIC_EGRESS`, VPC-SC, `constraints/cloudbuild.allowedWorkerPools` |
| S11 | https://docs.cloud.google.com/build/docs/build-config-file-schema | `cloudbuild` Docker network exposes ADC to steps |
| S12 | https://docs.cloud.google.com/build/docs/securing-builds/authorize-service-to-service-access | Build steps can mint tokens from the metadata server |
| S13 | https://docs.github.com/en/actions/reference/security/secure-use | Self-hosted runner hardening, JIT "at most one job", runner groups |
| S14 | https://docs.github.com/en/rest/actions/self-hosted-runners | `generate-jitconfig`: params, admin requirement, no statement on expiry |
| S15 | https://docs.github.com/en/rest/authentication/permissions-required-for-github-apps | Administration write = generate-jitconfig and also `DELETE /repos/{owner}/{repo}`, branch protection, rulesets |
| S16 | https://docs.github.com/en/actions/reference/runners/self-hosted-runners | Required runner hosts; "only the domains listed will remain constant" |
| S17 | https://docs.github.com/en/actions/how-tos/manage-runners/self-hosted-runners/remove-runners | Unused JIT runners "will automatically be removed" (no time given) |
| S18 | https://docs.github.com/en/actions/how-tos/manage-runners/self-hosted-runners/use-proxy-servers | Runner proxy env vars; lowercase wins over uppercase |
| S19 | https://docs.github.com/en/actions/reference/workflows-and-actions/dependency-caching | Cache scope rules, cache-poisoning guidance |
| S20 | https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries | HMAC, constant-time compare |
| S21 | https://docs.github.com/en/webhooks/using-webhooks/best-practices-for-using-webhooks | Replay and `X-GitHub-Delivery` |
| S22 | https://docs.github.com/en/webhooks/using-webhooks/handling-failed-webhook-deliveries | "GitHub does not automatically redeliver failed deliveries" |
| S23 | https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/enabling-features-for-your-repository/managing-github-actions-settings-for-a-repository | Fork PR policies for private repos |

I did not check these design claims and treat them as **assumed**: Cloud Build
pricing and free tier, default-pool vCPU limits (S9 confirms only the 30
concurrent builds maximum), the Direct VPC egress start-up delay, the cost
table for the fallback options, and Cloud NGFW domain-rule limits.

## 0.1 Where the design depends on a claim the documentation does not make

These are the first findings. Each one is a load-bearing sentence in the
design.

| # | Design claim | What the documentation actually says | Consequence |
| --- | --- | --- | --- |
| D1 | "nothing worth stealing is reachable from the sandbox" | S1/S2: the sandbox mounts **the host container's root filesystem read-only at `/` by default**. `--rootfs` "exposes readable host container files". S1: "Don't store sensitive files on the host container's disk where sandboxed processes can read them." | Anything the launcher, proxy or broker writes to the host filesystem can be read by job steps. That includes a JIT config written to a file, a gcloud or ADC token cache, broker temp files and build contexts from earlier calls. The no-metadata-server claim is documented. The no-secrets-reachable claim is **assumed**, and it depends on how the host image is built. |
| D2 | "The just-in-time configuration is single-use … it is spent when the runner starts" | S13: a JIT runner performs "at most one job". S17: unused JIT runners "will automatically be removed", with **no time given**. S14 says nothing about expiry or about a config being spent on connect. | The config is spent **when a job is assigned**, not when the runner starts. A config whose runner never took a job stays valid for an undocumented period. That period is exactly the idle-timeout case (§4, T5). "Spent at start" is **assumed**. |
| D3 | The sandbox is an isolation boundary strong enough to run job code next to the host identity | S1 says the sandbox "isolate[s] process execution" and that sandboxes are "completely isolated from each other". It does **not** name the isolation mechanism (gVisor, microVM or namespaces) and makes no escape-resistance claim. The feature is **Preview**, "as is", "might have limited support". Processes inside run "with `sudo` privileges". | How strong the boundary is remains **assumed**. The blast radius of an escape has to be designed for (T9). |
| D4 | The only bridge is the sandbox's stdin and stdout | S1/S2 document no egress by default and the stdio forwarding flags. They say nothing about loopback reachability between sandbox and host, or about whether the sandbox shares the host's PID or IPC namespaces or sees host `/proc`. The 9p and socket finding is **tested**, per the spike. | That no other host↔sandbox channel exists is **assumed**. This needs a negative test (Test 2). |
| D5 | "Cloud Run does not let containers add kernel capabilities … all containers share one network namespace" | S4 documents no capability changes. S4 also says containers run "under user, network, PID, and other Linux namespaces, isolating them from each other", and in the same page that sibling containers reach each other on `127.0.0.1`. The shared namespace is implied by the `/etc/hosts` section, not stated. | This does not weaken the design's conclusion that a sidecar proxy alone is advisory. That conclusion stays correct. |
| D6 | GitHub App needs "Administration write" and the key is "an admin credential" | S15: Administration write also authorises `DELETE /repos/{owner}/{repo}`, branch protection removal and ruleset changes on every installed repo. | "Admin credential" is accurate and understated. The key can delete or unprotect every installed repo. |
| D7 | "The reconciler … redelivers failed deliveries through the App's deliveries API" | S22 documents that GitHub does not redeliver, and that App webhook redelivery exists. The App-level deliveries endpoints authenticate with the App JWT, so whatever runs `/reconcile` holds the App private key. | Assumed to be the dispatcher. Note it, because it means the reconcile endpoint is also an App-key holder (T12). |
| D8 | "Cloud Build's default pool has open internet … only build steps from the repo's own Dockerfile run there" | S9: the default pool has public internet access (documented). S11/S12: build steps can obtain the build service account's token through the metadata server or the `cloudbuild` network. | "Only the repo's Dockerfile" is the whole attack surface, because the Dockerfile is job-controlled code. Whether a Dockerfile `RUN` can reach the build SA's credentials depends on the broker's build config, for example whether `--network=cloudbuild` is passed. **Not verified**. |
| D9 | Runner failure handling | S7: Cloud Run jobs retry a failed task **3 times by default**, with the same arguments. | The design does not mention task retries. Unless `maxRetries` is 0, a dead execution relaunches with the same JIT config (T5, T8). |
| D10 | Allowlist completeness | S16 also lists `release-assets.githubusercontent.com`, `github-registry-files.githubusercontent.com`, `*.pkg.github.com` and the LFS hosts. | This is about function, not security. It matters only because every host added later widens exfiltration (§5). |

## 1. Assets

| Asset | Where it lives | Why it matters |
| --- | --- | --- |
| **GitHub App private key** | Secret readable by the dispatcher SA. Also the reconciler (D7). | Administration write on every installed repo: mint runners, delete repos, remove branch protection (S15). Actions write: re-run jobs. The highest-value asset. |
| **Installation access tokens** | Dispatcher memory | One-hour version of the above, per installation. |
| **Webhook secret** | Receiver | Forging it lets anyone drive the dispatcher. |
| **JIT runner configs** (`encoded_jit_config`) | Dispatcher → `jobs.run` override → execution resource → host container args (and, if written there, host disk) | Whoever presents it can register as that runner and receive the **next matching job in the repo**, with its `GITHUB_TOKEN`, its secrets and its OIDC token. |
| **Runner job SA (host)** | Metadata server in the host container | Logs write. `cloudbuild.builds.create` plus actAs on the build SA, which means arbitrary build steps as the build SA. |
| **Build SA** | Cloud Build workers | Write to the Artifact Registry repo: image supply chain for every consumer of those images. |
| **Dispatcher SA** | Dispatcher | `run.jobs.runWithOverrides`, App key read. |
| **`GITHUB_TOKEN`** | Sandbox, per job | Repo write if the defaults allow it (S13). Push code, create releases, trigger workflows. |
| **Repository and environment secrets, OIDC tokens** | Sandbox, per job | Whatever cloud access the workflow already federates into. The design lists this as allowed for code in the sandbox. |
| **Source code** | Workspace on ephemeral disk, in the sandbox | Private repos. Confidentiality is the point of keeping them private. |
| **Caches**: GitHub Actions cache, and the Artifact Registry registry cache the shim substitutes for `type=gha` | GitHub (per repo/branch, S19), Artifact Registry (scope set by the broker) | Poisoning runs code in later, more privileged jobs. |
| **Build outputs**: images in Artifact Registry, artifacts, releases | Artifact Registry, GitHub | Downstream deploys trust them. |
| **The bill**: Cloud Run vCPU-seconds, Cloud Build minutes, AR storage, egress | Project | Denial of wallet. |
| **Availability of CI** | Whole system | Mitigated by the `vars.RUNNER` fallback. |

## 2. Trust boundaries

```text
                         (A) internet ↔ receiver
 GitHub ──webhook──────────────▶ receiver (public Cloud Run service)
   ▲  ▲                           │ HMAC verify, label/installation filter
   │  │                           ▼            (B) receiver ↔ dispatcher
   │  │                        Pub/Sub topic ──push──▶ dispatcher (private)
   │  │  (C) dispatcher ↔ GitHub   ◀── App JWT / installation token ──┘   │
   │  └─ generate-jitconfig, list jobs, re-run, redeliver                  │ jobs.run + args override
   │                                                                       ▼   (JIT config crosses here,
   │                                                         Cloud Run execution  and is stored on the
   │                                                         ┌──────────────────────────────┐ execution resource)
   │                                                         │ host container (runner SA,   │
   │                                                         │ metadata server reachable)   │
   │       (E) proxy ↔ internet                              │  launcher  proxy  broker     │──(F) broker ↔ Cloud Build──▶ Cloud Build
   └──────────────── allowlisted CONNECT ◀──────────────────│    ▲ stdio-bridge (demux)    │      (default pool, open
                                                             │════╪═════════════════════════│       internet, build SA)
                                                             │ (D) host ↔ sandbox          │            │
                                                             │  ▼  stdin/stdout only (D4)  │            ▼
                                                             │ sandbox: runner + steps     │      Artifact Registry
                                                             │ no egress, no MDS (S1)      │
                                                             │ host rootfs READ-ONLY (D1)  │
                                                             └──────────────────────────────┘
```

| Boundary | What crosses | Evidence it holds |
| --- | --- | --- |
| A internet ↔ receiver | Arbitrary HTTP. Only signed `workflow_job` should pass. | HMAC with constant-time compare follows S20 (**documented** practice). The receiver's implementation is **assumed** until tested. |
| B receiver ↔ dispatcher | Pub/Sub messages | Pub/Sub push auth (OIDC on push) is **assumed**. The design does not say the dispatcher verifies the push token. |
| C dispatcher ↔ GitHub | App JWT, installation tokens, JIT configs | GitHub's TLS and auth. The App's permission scope is **documented** (S15). |
| D host ↔ sandbox | stdio frames, bind-mounted workspace and socket dir, **read-only view of host rootfs** | No egress and no MDS are **documented** (S1). That stdio is the only channel is **assumed** (D4). The rootfs exposure is **documented** and is a hole by default (D1). |
| E sandbox ↔ proxy ↔ internet | CONNECT to allowlisted names | Filter correctness is **assumed** until tested (Test 3). It is not an exfiltration boundary (§5). |
| F broker ↔ Cloud Build | Build context, build config, push targets | Broker validation is **assumed**. The default pool's open egress is **documented** (S9). |

## 3. Adversaries

1. **Malicious step in a trusted repo** (compromised npm, PyPI or Go
   dependency, or a compromised third-party action). Runs as the job, inside
   the sandbox, with the job's `GITHUB_TOKEN`, secrets and OIDC. This is the
   **primary** adversary the sandbox exists for.
2. **Malicious pull request author.** The design targets **private** repos.
   - A collaborator with write access is adversary 1 with intent. They can
     also add `runs-on: cloudrun-2` directly, so `vars.RUNNER` is not an
     access control.
   - A **fork PR author** is admitted **only if** the repo or org enables "Run
     workflows from fork pull requests" for private repos (S23). With that on,
     they get a read-only token and no secrets unless "Send write
     tokens/secrets" is also on. The design neither states nor enforces that
     these stay off. **Admitted: no by default, yes by configuration.** The
     receiver does not check the event's actor or head-repo, so nothing in
     the design refuses them. The design should state that it does not admit
     them (R7).
3. **Webhook forger.** Without the secret: blocked by HMAC (assumed until
   Test 6). With a captured legitimate delivery: replay, which dedup on
   `workflow_job.id` absorbs (S21 recommends `X-GitHub-Delivery`). With the
   secret: can trigger launches for any allowlisted installation and repo,
   which is denial of wallet. It **cannot** choose which job a runner takes,
   but it **can** create surplus runners that take real queued jobs (T6).
4. **Reader of the Cloud Run project.** Anyone with `run.executions.get`. Per
   S6 that includes basic `roles/viewer`/Reader, `roles/run.viewer`,
   `iam.securityAuditor`, `iam.supportUser` and others. They can read override
   args, and with them the JIT config (T4). Holders of `run.jobs.sshRoot`
   (`roles/run.developer` and above, S6; "Beta") can presumably open a root
   shell in a running execution's host container. That would hand them the
   host SA and the bridge. Whether SSH works on a sandbox-enabled job
   execution is **not verified**. Readers of Cloud Logging see anything the
   launcher forwards from the runner.
5. **Compromised upstream**: GitHub, a Google Preview feature regression, a
   compromised runner release. Mostly accepted. Listed so that "Preview
   features changing under us" has an owner.

## 4. Threats

Legend for the last column: **T** = tested (per spike), **D** = documented,
**A** = assumed. The label applies to the existing control, not to the threat.

| ID | Threat | Boundary | Precondition | Impact | Existing control | Residual risk | Control evidence |
| --- | --- | --- | --- | --- | --- | --- | --- |
| T1 | **Exfiltration to `*.blob.core.windows.net`**: step uploads source or secrets to an attacker-owned Azure storage account | E | Adversary 1 | Full loss of source and secrets the job can see | None. The wildcard is required (S16). | **High**: the design accepts it | D (the requirement is documented) |
| T2 | **Exfiltration over `github.com` / `api.github.com`**: push to an attacker repo using attacker credentials carried in the payload, gists, issues on a public repo, or the job's own token to a repo it can write | E | Adversary 1 | Same as T1. Also repo tampering with the job's own token. | Least-privilege `GITHUB_TOKEN` is recommended (S13). Not enforced by the design. | **High** | A |
| T3 | **Exfiltration and supply-chain attack via package registries**: publish to npm or PyPI with attacker credentials, or with the repo's publish token if the job has one | E | Adversary 1 | Data loss. Malicious package published under the org's name if publish tokens are in scope. | None | **Medium–High** | A |
| T4 | **JIT config read from execution arguments** | Project read → C | Adversary 4 (any `roles/viewer`), during the window between `jobs.run` and job assignment, or indefinitely if the runner never took a job (D2) | Attacker registers a runner and takes the next queued job in that repo: its `GITHUB_TOKEN`, secrets and OIDC, **outside** the sandbox and the proxy | "Single-use" (D2: spent only at assignment). IAM on the project. | **High** while viewers are broad. Race-limited for executions that start quickly. | A (spent-at-start is not documented) |
| T5 | **Leftover idle runner takes a later job.** Launcher exits after 10 idle minutes, but the GitHub registration remains until GitHub's unspecified cleanup (S17). Cloud Run task retry (S7, default 3) relaunches with the same config. | C | Job cancelled, duplicate launch, or dispatcher over-count | (a) A retried task registers late and takes a job meant for another runner. This is benign, because all runners are equivalent and fresh. (b) The **config remains usable by T4's attacker** after the legitimate runner is gone. | Idle timeout. Count-based reconcile. | **Medium**: the benign case is fine, the T4 amplification is not | A |
| T6 | **Surplus runner takes a privileged job**: a JIT runner is not bound to the job that triggered it (design). A runner launched for a low-trust event can take a deploy job in the same repo. | C | Same repo, matching labels | None beyond the job's own trust, **provided** every runner is fresh and identical. It is exploitable only combined with T4 or T5. | One execution per runner, fresh sandbox | **Low** | D (S13 at-most-one-job), T (fresh execution) |
| T7 | **Metadata server or host identity from the sandbox**: direct HTTP to `169.254.169.254` | D | Adversary 1 | Runner SA token, then builds as build SA, then AR write | Sandbox has no MDS by default | **Low** for the direct path | D (S1), not yet T |
| T7b | **Metadata server via the proxy (SSRF)**: the proxy runs in the host container, which *can* reach the MDS. A plain-HTTP absolute-URI request, a CONNECT to `metadata.google.internal:80`, an IP literal, an allowlisted name that resolves to link-local, or a sloppy suffix match (`evilgithub.com` matching `github.com`) reaches it. | D→E | Proxy bug or loose matching | Runner SA token in the sandbox | Hostname allowlist on CONNECT | **Medium until tested**: this is the most likely real route to the host identity | A |
| T7c | **Host identity via the host filesystem (D1)**: gcloud/ADC token cache, the JIT config file, broker temp files, other jobs' contexts (none, since each execution holds one job) | D | Host writes credentials to disk outside paths the sandbox can't see | Runner SA token, valid for up to about an hour | None stated | **Medium** | A |
| T7d | **Host compromise via `stdio-bridge` or broker parsing**: sandbox-controlled bytes are demultiplexed and parsed in the host. Build context tar is extracted on the host (S1 and S2 warn about symlinks). | D | Parser or extraction bug | Code execution in the host container, which equals a sandbox escape (T9) | None stated | **Medium** | A |
| T8 | **Broker as confused deputy** | F | Adversary 1 calls `127.0.0.1:3129` with crafted requests | (a) Push to **any tag** in the shared AR repo, including another repo's image or `:latest`, if one AR repo serves all repos. (b) Write the registry cache another repo or branch reads (T10). (c) Pass build options such as `--network=cloudbuild`, `--secret`, a custom `cloudbuild.yaml`, or a different builder image, to get build-SA credentials (S11/S12). (d) Unbounded builds (T11). (e) Context with symlinks or `..` escapes the broker's working directory on the host (T7d). | Wrapper limits verbs. "Pushes go to Artifact Registry only." | **High** unless the broker binds every request to the job's repo, ref and image name | A |
| T9 | **Sandbox escape** | D | Kernel or sandbox-runtime bug. Preview product (D3). | Host container: runner SA (logs, submit builds as build SA, so AR write), unfiltered egress (the host has default egress), the bridge. **Not** the App key, the dispatcher SA, or other jobs, because each execution runs one job and nothing is co-tenant **within** the instance. Escape beyond the instance to Google's infrastructure is out of scope. | Sandbox isolation (S1, mechanism undocumented) | **Low likelihood, medium impact**. The impact is bounded mainly by the build SA's AR write and the absence of any other identity in the host. | D (claims), A (strength) |
| T10 | **Cache poisoning** | E, F | (a) GitHub cache: a job on a feature branch can't write the default-branch scope, and `pull_request` writes only the merge ref (S19, documented). The cache is per repo and shared by **hosted and self-hosted** runners alike, so moving `vars.RUNNER` back and forth changes nothing. Self-hosted runners don't add cross-runner risk. A workflow on an untrusted trigger that declares `cache-mode: write` reintroduces poisoning (S19). (b) **AR registry cache (new)**: its scope is whatever the broker uses as `cache-to`/`cache-from`. If keyed by repo only, a PR or feature-branch build writes layers that `main` builds then trust. GitHub's branch scoping does not apply. | Adversary 1 on a low-trust branch | Code execution in later privileged builds and images | GitHub scoping (D). Nothing for AR (A). | **Medium–High** for the AR cache | D / A |
| T11 | **Denial of wallet** | A, B, F | 200-job matrix. Workflow loops via `GITHUB_TOKEN` (`workflow_dispatch`, re-runs). Forged or replayed webhooks if the secret leaks. Broker build loops. Stuck executions. | Bill. Ephemeral-disk regional quota (100 GiB default, S8) and Cloud Build concurrency (30, S9) exhausted, so CI stalls. | Per-repo concurrency cap. Cloud Run quotas. 10-minute idle exit. `timeout-minutes: 20` in workflows (not enforced). Execution timeout (value not stated). | **Medium**: there is **no cap on broker builds**, and the execution timeout is unspecified | A |
| T12 | **App key theft** | C | Compromise of the dispatcher or reconciler, or of anyone with `secretAccessor` on it | Delete or unprotect every installed repo (S15). Mint runners. Push via re-run. | IAM: only the dispatcher SA | **Medium**: the impact is the worst in the system | A |
| T13 | **Cloud Build default pool egress bypasses the proxy** | F | Adversary 1 writes a Dockerfile `RUN curl …` | Exfiltration of the build context, the source, anything in build args. Possibly build-SA token (D8). | None. The design accepts it. | **High** for exfiltration (equal to T1, so it adds little *exfiltration* risk). **Medium** for credential exposure, and that part is *new* | D (S9) |
| T14 | **Forged or abused webhooks** | A | No secret. Or replay. | DoS or spend | HMAC, body cap, label and installation filter, dedup | **Low** | A until Test 6 |
| T15 | **Secrets in logs**: the launcher forwards runner stdout/stderr or `_diag` output to Cloud Logging, readable by project log viewers | D→project | Default launcher behaviour | Secret disclosure to adversary 4. GitHub's masking applies to GitHub's log stream, not to the runner's local diagnostics. | None stated | **Medium** | A |
| T16 | **Fork PR workflows enabled** on a private repo puts outsiders on these runners | A/C | Repo or org setting (S23) | Adversary 2 becomes adversary 1 (read-only token, no secrets unless the other toggles are on) | None in the design | **Low–Medium** | A |

## 5. What the allowlist does not do

**The allowlist prevents direct egress. It does not prevent exfiltration.**

- *Direct egress prevention* means a process in the sandbox cannot open a
  connection to an arbitrary host. The sandbox's lack of a network enforces
  that, which is **documented** (S1). The proxy then decides which names a
  CONNECT may reach. This stops a naive payload calling home to
  `attacker.example`. It also stops raw TCP, DNS tunnelling to the attacker's
  resolver and non-HTTPS protocols.
- *Exfiltration prevention* would mean data cannot leave to a party not
  entitled to it. The allowlist cannot do that, because every host GitHub
  requires is a multi-tenant write service. `*.blob.core.windows.net` accepts
  uploads to *any* Azure storage account. `github.com` accepts pushes with
  *any* credentials the payload carries. npm and PyPI accept publishes from
  any account. A hostname filter without TLS inspection cannot see which
  account or tenant is behind a name. Even TLS inspection could not separate
  "GitHub's blob account" from "attacker's blob account" on the wildcard
  without a list of GitHub's account names, which GitHub does not promise
  (S16: only the listed domains stay constant).
- Cloud Build's default pool (T13) is a second unfiltered path, and it is
  outside the sandbox.
- The allowlist does not limit **what the job's own credentials can do** on
  allowed hosts: `GITHUB_TOKEN`, OIDC into a cloud, registry publish tokens.
  Those are limited only by least privilege in the workflow.
- The allowlist does not protect against code that is malicious *by
  design* within allowed behaviour, such as a poisoned build output pushed to
  AR (T8, T10).

So the security property the design can claim is: **a compromised step can
steal what the job can see, and can act with what the job holds. It cannot
reach the runner's cloud identity, the App key or other jobs.** That claim
holds only if D1, D4, T7b, T7c and T8 are closed. Today they are assumed.

## 6. Recommendations (ranked by risk reduced per unit of effort)

| Rank | Recommendation | Kind | Addresses | Effort |
| --- | --- | --- | --- | --- |
| R1 | Set the runner jobs' **`maxRetries` to 0**. A runner job must never be retried with the same JIT config. The dispatcher's "re-run once on `completed`" is the only retry path. | Design change | T5, D9 | Trivial |
| R2 | Launch the sandbox with **`--rootfs` pointing at a dedicated minimal runner root**, not `/`. Keep every host credential, config and temp path outside it. Never write the JIT config or any token to a path the sandbox can see. Read the JIT config from args into memory only. Do not use gcloud CLI token caches in the host: use the metadata server directly from the broker. | Design change + test (Test 1) | D1, T7c | Low |
| R3 | **Bind the broker to the job.** Derive the permitted image path from the repo (for example `…/ci/<repo>/<image>`). Refuse any other push target. Refuse user-supplied `cloudbuild.yaml`, builder images, `--network` and `--secret`. Generate the build config in the broker. Scope `cache-to` to repo **and** ref, and let `cache-from` read only own-ref and default-branch. Cap builds per execution (count and minutes). Stream the context to GCS without extracting it on the host. Per S1/S2, never `tar -xf` sandbox-supplied archives in the host. Prefer per-repo AR repos with per-repo build SAs if the repo count is small. | Design change | T8, T10b, T11, T7d | Medium |
| R4 | **Proxy hardening plus a test**: CONNECT only, port 443 only, exact or `*.`-label wildcard matching (no substring or suffix), refuse IP literals, resolve and refuse private, link-local and metadata addresses after DNS, refuse plain-HTTP absolute-URI requests. | Design change + test (Test 3) | T7b | Low |
| R5 | **Remove broad viewers from the runner project.** Put runner jobs in a dedicated project with no `roles/viewer`, `run.viewer` or Reader grants beyond the operators. Also remove `run.jobs.sshRoot`/`sshRead` from everyone but break-glass. | Design change | T4, adversary 4 | Low |
| R6 | **Delete unused JIT runner registrations.** When an execution exits without taking a job (the launcher's idle exit, a crash before assignment), the dispatcher or reconciler calls the "delete a self-hosted runner" API for that runner name. The reconciler already compares launched runners with idle ones, so it knows which. Verify how long GitHub keeps an unused JIT registration (S17 gives no number). | Design change + doc claim to verify | T4, T5, D2 | Low |
| R7 | **State and enforce the admission policy**: fork PR workflows stay off for these repos, or the receiver refuses jobs whose run comes from a fork. Workflows set `permissions:` to read-only by default (S13). Audit both with the repo-settings sweep. | Design change (docs + check) | T16, T2 | Low |
| R8 | **Do not forward runner output to Cloud Logging verbatim.** Forward only launcher and lifecycle lines. Restrict log viewers on the runner project. | Design change | T15 | Low |
| R9 | **Set an explicit execution timeout** per size class (for example job `timeout-minutes` plus a margin, at most 60 minutes). Add a **Cloud Build budget**: builds per execution, and a project-level budget alert with a kill switch. Add a global, not only per-repo, cap on concurrent executions in the dispatcher. | Design change | T11 | Low |
| R10 | **Narrow the App key**: hold it in a separate minting function. The dispatcher asks it for "JIT config for repo X", and the reconciler uses only a read-scoped path. The key never leaves that function. GitHub Apps cannot be restricted below Administration write for this endpoint (S15), so isolation is the only lever. | Design change | T12 | Medium |
| R11 | **Private Cloud Build pool** with `NO_PUBLIC_EGRESS` or VPC egress through the same proxy, and enforce it with `constraints/cloudbuild.allowedWorkerPools` (S10). Until then, verify (Test 5) that a Dockerfile `RUN` cannot obtain a build-SA token. | Design change (v2) + test now | T13, D8 | Medium–High |
| R12 | Verify that the dispatcher checks the **Pub/Sub push OIDC token** (audience and service account). | Doc claim to verify / test | B | Trivial |
| R13 | Track the sandbox's **launch stage and release notes** (Preview, S1). Re-run Tests 1–3 on every runner-image build, because sandbox behaviour can change without a deploy on our side. | Test (CI) | D3, D4 | Low |

## 7. Tests that would prove the key controls

Each test has a **negative control**, a deliberately broken variant that must
make the test fail. Without one, a passing test proves only that the probe
cannot see anything.

| # | Control | Test (inside a real runner execution, as a workflow step) | Negative control (must fail the test) |
| --- | --- | --- | --- |
| 1 | No host secrets readable from the sandbox (D1, R2) | Before starting the sandbox, the launcher writes a canary file with a random value to every host path it uses (config, temp, home and broker workdir), and holds the JIT config only in memory. A step runs `grep -r <canary> / 2>/dev/null` and also reads `/proc/*/cmdline` and `/proc/*/environ`, looking for the canary and `encoded_jit_config` fragments. Pass = no match. | Run the same probe in a variant where the launcher uses the default `--rootfs=/`, or puts the canary under the sandbox-visible root. The probe **must** find it. |
| 2 | No network and no metadata server from the sandbox (S1, D4) | A step, with proxy env vars unset, tries: `curl -m5 http://169.254.169.254/computeMetadata/v1/ -H 'Metadata-Flavor: Google'`, `curl -m5 https://example.com`, a TCP connect to 8.8.8.8:53, DNS resolution of an attacker-controlled name, and connecting to every host-container listening port on `127.0.0.1` other than through the bridge. Pass = all fail. | The same job with the sandbox started with `--allow-egress`, or the identical probe run in the host container: the MDS call and the direct HTTPS **must** succeed. |
| 3 | Proxy enforces the allowlist and cannot be used as SSRF (T7b, R4) | Through `127.0.0.1:3128`: CONNECT `api.github.com:443` (must pass). Then CONNECT `example.com:443`, `metadata.google.internal:80`, `169.254.169.254:443`, `github.com:22`, `evilgithub.com:443`, `github.com.attacker.example:443`, `[::ffff:169.254.169.254]:443`, plain `GET http://metadata.google.internal/…`, and a name you control that resolves to `169.254.169.254` (must all be refused). | Swap in a proxy build with the allowlist set to `*` or with substring matching. The SSRF and suffix probes **must** succeed, which shows the probes reach the proxy and can detect a pass. |
| 4 | Broker refuses out-of-scope pushes and options (T8, R3) | From the sandbox, call the broker to (a) push to another repo's image path, (b) push to a non-AR registry, (c) pass `--network=cloudbuild`, (d) submit a custom build config, (e) send a context with a symlink to `/etc/passwd` and a `../` entry, (f) exceed the per-execution build cap. Pass = every request refused before Cloud Build is called (check Cloud Build's build list for no new builds). | A build for the job's own image path with a normal context **must** succeed and appear in Cloud Build. Point (a) at a broker variant with the path check disabled: it **must** push. |
| 5 | Cloud Build steps cannot obtain the build-SA token (D8) | Submit a Dockerfile through the shim with `RUN curl -s -H 'Metadata-Flavor: Google' http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token \|\| true` writing the result to a layer file. Pass = no token. Expect `RUN curl https://example.com` to **succeed** on the default pool. That is T13, and recording it keeps the accepted gap visible. | The same Dockerfile built with `docker build --network=cloudbuild` in a hand-written Cloud Build config **must** return a token (S11). |
| 6 | Receiver rejects forged and replayed webhooks (T14) | Send (a) a body with a wrong signature, (b) a valid signature over a modified body, (c) an oversized body, (d) an exact replay of a valid delivery, (e) a valid delivery for an unknown installation, (f) a label off the allowlist. Pass = no Pub/Sub message, or no extra launch for (d). | A correctly signed fresh `queued` delivery for an allowlisted label **must** produce exactly one launch. |
| 7 | JIT config is not reusable (D2, R6) | Capture a JIT config from an execution whose runner exited idle (no job). After the launcher's exit and the R6 deletion, start a runner elsewhere with that config and queue a job. Pass = the runner fails to register or connect. | The same capture *without* R6's deletion, inside GitHub's undocumented cleanup window. Expected to **connect**. If it does not, document that GitHub invalidates promptly and that R6 is belt-and-braces. |
| 8 | No task retry reuses a config (R1) | Kill the host container mid-job (`kill -9` from the launcher on a test flag). Pass = Cloud Run reports the execution failed with 0 retries, and the dispatcher's re-run path fires exactly once. | The same with `maxRetries: 3`: a second task attempt **must** appear with the same args. |

## 8. Future mitigations (speculative, with their limits)

- **Per-job workload identity** (minting a short-lived, job-scoped GCP
  identity for the broker per execution). This limits a T9 escape to one
  job's scope. Limitation: Cloud Run gives an execution one service account
  for its whole duration. Per-job identities would need the dispatcher to
  impersonate or mint tokens and hand them to the host, which reintroduces a
  token on the host (D1). **Not verified** that any native Cloud Run feature
  does this.
- **SPIFFE-style identities for executions**. Not a Cloud Run feature as far
  as I could find (**not verified**). It would need our own issuer and offers
  nothing over per-job service accounts here.
- **VPC Service Controls** around the runner project. These protect **Google
  APIs** (AR, GCS, Cloud Build, Secret Manager) from being called by a stolen
  token from outside the perimeter. They do **not** filter traffic to
  arbitrary internet hosts: GitHub, blob storage and npm are untouched. So
  they mitigate T7/T9 token theft, not T1–T3. They also apply to Cloud Build
  only with private pools (S10).
- **TLS-inspecting proxy** (for example Secure Web Proxy, or our own MITM CA)
  to restrict blob uploads to GitHub's storage accounts. Limitation: GitHub
  does not publish those account names (S16). It requires a CA in the
  sandbox, which the design deliberately avoids, and it breaks certificate
  pinning in some tools.
- **Private Cloud Build pool behind the proxy** (R11). This is real and
  documented (S10). It is listed here only because it costs a VPC and
  removes the zero-infrastructure benefit.

## 9. Final report

### Five highest residual risks

| # | Risk | Why it ranks here | Cheapest reduction |
| --- | --- | --- | --- |
| 1 | **Exfiltration over required hosts** (T1–T3, T13): source and secrets leave via `*.blob.core.windows.net`, `github.com`, registries or Cloud Build's open egress | The design accepts it and no hostname allowlist can close it (§5). Likelihood rises with every dependency. | Least-privilege tokens and secrets per job (R7). Accept the rest explicitly. |
| 2 | **Broker as confused deputy** (T8, T10b): job pushes to another repo's image or poisons a shared AR cache | Turns one compromised dependency into a supply-chain compromise of *other* repos' images. No control exists in the design. | Bind push path and cache scope to repo and ref, and generate the build config in the broker (R3). |
| 3 | **JIT config exposure** (T4, T5, D2): any project viewer can take a config from execution args and steal a real job's token and secrets outside the sandbox. Unused configs live for an undocumented time. Default task retries reuse them. | "Single-use, spent at start" is not what GitHub documents. `roles/viewer` is common. | `maxRetries: 0` (R1), dedicated project with no broad viewers (R5), delete unused registrations (R6). |
| 4 | **Host identity reachable via the read-only host rootfs or the proxy** (D1, T7b, T7c) | The sandbox's documented default exposes the host's files. The proxy itself can reach the MDS. Either gives the runner SA, and through it the build SA and AR write. | `--rootfs` to a clean root, no on-disk tokens (R2), strict proxy (R4), proven by Tests 1 and 3. |
| 5 | **Sandbox strength is undocumented and the feature is Preview** (D3, T9) | An escape gives the host SA and unfiltered egress. Google states no isolation mechanism and offers no support commitment. | Keep the host identity minimal (R2/R3) so an escape is worth little. Re-run Tests 1–3 on every image build (R13). |

### The three tests to run first

1. **Test 1, host rootfs canary.** It checks the most likely way the design's
   central claim ("nothing worth stealing is reachable") is false by default,
   and it costs one step.
2. **Test 3, proxy SSRF and allowlist probes.** The proxy is the only
   component in the host that both takes sandbox input and can reach the
   metadata server.
3. **Test 4, broker scope probes.** These turn the biggest design gap into a
   pass/fail before any second repo shares the Artifact Registry repo.
