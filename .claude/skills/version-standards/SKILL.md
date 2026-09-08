---
name: version-standards
description: Canonical versions of Node.js, pnpm, Go (toolchain plus chi, sqlc, pgx, goose, golangci-lint and the test stack), and GitHub Actions (incl. self-hosted runners) for all projects. Covers the exact-dependencies rule (no ^ or ~), the runner strategy (hybrid self-hosted), and the pnpm migration guide. Consult when creating or modifying workflows, package.json, go.mod, .golangci.yml, Dockerfiles, or auditing existing projects.
---

# Stack Version Standards

Consult this skill whenever you:

- Create or modify a GitHub Actions workflow
- Create or modify a `package.json`
- Create or modify a `go.mod` or a `.golangci.yml`
- Create or modify a `Dockerfile` with Node or Go
- Audit an existing project to update its stack

---

## Canonical versions (June 2026)

| Tool | Standard version | EOL / next change |
|---|---|---|
| **Node.js** | `24` (Active LTS) | April 2028. Evaluate Node 26 once it reaches LTS (Oct 2026) |
| **pnpm** | `10` (latest 10.x patch) | Migrate to pnpm 11 (see migration section) |
| **`@playwright/test` / `playwright`** | `1.60.0` (exact) | No LTS (~1 minor/month). Review every ~6 months (next: Q4 2026), see Rule 5 |
| `actions/checkout` | `v7` | v7.0.1 |
| `actions/setup-node` | `v7` | v7.0.0 |
| `actions/setup-go` | `v7` | v7.0.0 |
| `pnpm/action-setup` | `v6` | v6.0.10 |
| `docker/build-push-action` | `v7` | v7.3.0 |
| `docker/login-action` | `v4` | v4.6.0 |
| `docker/metadata-action` | `v6` | v6.2.0 |
| `docker/setup-buildx-action` | `v4` | v4.3.0 |

Actions verified against `gh api repos/<owner>/<repo>/releases/latest` on **2026-08-23** (`actions/setup-node` row bumped to v7.0.0 on **2026-08-24** after changelog review, see Rule 3). This table records **which version to target**, not how to write it in a workflow: an action is pinned by **full-length commit SHA plus an inline `# vX.Y.Z` comment**, never by a floating tag (see Rule 6). The last column records the concrete patch current as of verification. **When bumping a major** (e.g. `checkout@v4`→`@v7`), review the repo's changelog: a major can bring breaking changes (cache defaults, the action's runtime Node version, renamed inputs). Revalidate with `gh api` every ~6 months.

To check the latest exact pnpm 10 patch: `npm view pnpm@10 version` or [pnpm.io/installation](https://pnpm.io/installation).
To check Playwright's stable version: `npm view @playwright/test version` (returns the `latest` dist-tag = stable; pre-releases live under `next`/`beta`/`alpha`).

---

## Rule 1: No caret or tilde in dependencies

**Applies to:** `dependencies`, `devDependencies`, `peerDependencies`, `optionalDependencies`.

```json
// Forbidden
"@nestjs/common": "^11.1.12"
"typescript": "~5.4.0"

// Correct
"@nestjs/common": "11.1.12"
"typescript": "5.4.0"
```

**Reason:** `^` and `~` let `pnpm install` resolve different versions at different times. In CI, staging, or production, a Docker image can end up with different dependencies than what was tested locally: a security risk and a source of silent regressions.

**For new projects:** add this to the `.npmrc` at the repo root:

```
save-exact=true
```

This makes `pnpm add` always record exact versions without needing to pass `--save-exact`.

**For existing projects:** convert current dependencies to exact versions using the versions already resolved in the lockfile:

```bash
# Pin the installed versions into package.json
pnpm list --json --depth=0 | node -e "
const data = JSON.parse(require('fs').readFileSync('/dev/stdin','utf8'));
const deps = data[0].dependencies || {};
const pkg = JSON.parse(require('fs').readFileSync('package.json','utf8'));
['dependencies','devDependencies','peerDependencies','optionalDependencies'].forEach(t => {
  if (!pkg[t]) return;
  Object.keys(pkg[t]).forEach(name => {
    if (deps[name]) pkg[t][name] = deps[name].version;
  });
});
require('fs').writeFileSync('package.json', JSON.stringify(pkg, null, 2) + '\n');
console.log('Done: exact versions written to package.json');
"

# Verify no caret or tilde remains
grep -E '": "[\^~]' package.json && echo "Non-exact versions still remain" || echo "All versions are exact"
```

---

## Rule 2: Required fields in `package.json`

Every Node project must declare:

```json
{
  "engines": {
    "node": ">=24.0.0",
    "pnpm": ">=10.0.0"
  },
  "packageManager": "pnpm@10.x.x"
}
```

Replace `10.x.x` with the exact version the project uses (check with `pnpm --version`).

---

## Rule 3: Standard setup in GitHub Actions

The `packageManager` field in `package.json` is the source of truth for the pnpm version. **Do not declare `version:` in `pnpm/action-setup`**. That combination causes the `Multiple versions of pnpm specified` error, which blocks CI.

```yaml
- uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1

- uses: pnpm/action-setup@0977fd99725f1db4007ccb2928dbb4e90d06cc86 # v6.0.10
  # No `version:`, pnpm/action-setup reads it from packageManager in package.json

- uses: actions/setup-node@820762786026740c76f36085b0efc47a31fe5020 # v7.0.0
  with:
    node-version: '24'
    cache: 'pnpm'

- run: pnpm install --frozen-lockfile
```

Those three SHAs are real, resolved on **2026-08-24** for the versions in the canonical table. They are a point-in-time example: re-resolve before pinning, per Rule 6.

**Node in Dockerfile:**

```dockerfile
FROM node:24-alpine
```

---

## Rule 4: Runners (hybrid self-hosted)

**Hybrid** strategy: **private** repos run on the VPS self-hosted runner; **public** repos ALWAYS run on GitHub-hosted.

| Repo type | `runs-on` | Reason |
|---|---|---|
| **Private** | `[self-hosted, linux, vps-tunodo]` | No GitHub minutes consumed; runs on our own infra |
| **Public** | `ubuntu-latest` | **NEVER self-hosted**: a third party's PR would execute arbitrary code on the VPS (RCE) |

**Canonical label:** always use the full set `[self-hosted, linux, vps-tunodo]`, not just `self-hosted`, so routing and adding more runners later stays possible.

```yaml
jobs:
  ci:
    runs-on: [self-hosted, linux, vps-tunodo]   # private repo
    # public repo → runs-on: ubuntu-latest
```

**Execution model: ephemeral in a container.** Each job runs in a fresh container that is destroyed when it finishes. Benefits: no state contamination between runs and no disk buildup on the VPS host (a known issue with the host).

Implications for workflows:

- **Caching:** the filesystem does not persist between jobs. Use `actions/cache` (with a configured backend) or a mounted volume for the pnpm store. For Playwright binaries, prefer **baking them into the runner image** (see Rule 5) rather than a volume. `setup-node`'s `cache: 'pnpm'` still works if a cache backend is present.
- **Caching (Go):** `actions/setup-go` already handles this through its `cache: true` input (the default whenever a `go.mod` is present), caching the module cache `~/go/pkg/mod` and the build cache `~/.cache/go-build`. Unlike Playwright's browsers (Rule 5), a Go module cache is cheap to repopulate, so let the action do it per job instead of baking it into the runner image. Same caveat as the pnpm store above: `setup-go` caches through `actions/cache`, so it needs a cache backend present on the self-hosted runner.
- **Resources:** size `workers`/parallelism to the runner's container, not to GitHub-hosted's fixed 2 vCPUs.
- **Docker build:** the ephemeral container has no daemon of its own; use the mounted Docker socket or a DinD service. The `type=gha` layer cache may not be available. Prefer `type=registry` or `type=local` with a volume.
- **Secrets:** the runner already sits inside the VPS network → it can reach Infisical locally. See the **infisical-config** skill.

**Pre-check:** a workflow that depends on the self-hosted runner stays *queued* indefinitely if the runner is offline. Before relying on CI for something (e.g. Dependabot auto-merge), confirm the runner is online.

---

## Rule 5: Playwright (pinned version + runner)

**Canonical version: `@playwright/test` = `1.60.0`, exact** (no `^`/`~`, like every dependency, per Rule 1).

**No LTS.** Playwright ships ~1 minor release per month and bundles its own Chromium. Pinning *forever* has a real cost: the bundled browser ages and drifts away from the Chrome users actually run. So the policy is NOT "never change," but **exact pin + review every ~6 months** (next: Q4 2026), or sooner if a security fix or needed feature comes up. Check the stable release with `npm view @playwright/test version`.

**Double pin (critical with a self-hosted runner):** when the runner bakes the Playwright browser into its image, the version ends up pinned in **two places that MUST match**:

1. `@playwright/test` in the project's `package.json`.
2. `playwright@<version>` in the self-hosted runner's `Dockerfile`.

If they don't match, the job **re-downloads** the browser at runtime and loses the "install-once" benefit. **When bumping the Playwright version, rebuild the runner image.**

**Browser baked in, not on a volume.** In the runner's Dockerfile, install deps + browser to a fixed path and expose it via an env var, so the job downloads nothing (not even the first time):

```dockerfile
ENV PLAYWRIGHT_BROWSERS_PATH=/ms-playwright
RUN npx -y playwright@1.60.0 install --with-deps chromium \
  && chmod -R a+rx /ms-playwright
```

Do NOT mount a volume over `/ms-playwright` (it would shadow the baked-in browser). In the workflow, keep `playwright install chromium` (without `--with-deps`) as a safety net: it's an instant no-op when the version matches, and a one-time download if there's drift. The runner's user has no privileges → `--with-deps` (which runs `apt-get`) only works at build time as root, never inside the job.

---

## Rule 6: Pin GitHub Actions by SHA, not by tag

Every `uses:` in a workflow points at a **full-length (40 hex characters) commit SHA**, followed by an inline comment naming the version that SHA corresponds to.

```yaml
# Forbidden
- uses: actions/checkout@v7
- uses: actions/checkout@v7.0.0

# Correct
- uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0
```

**Reason:** a git tag is mutable. Whoever controls the action's repository (or an attacker who compromises it) can repoint `v7` at a different commit, and every consumer pinned to `@v7` picks up the new code with zero visibility. Not theoretical: in the **tj-actions/changed-files** incident (March 2025) the attacker moved the tags, major-version tags included, onto a malicious commit that dumped CI secrets into public build logs, hitting every repo that was pinned by tag. `reviewdog/action-setup` was hit the same way around the same time. A commit SHA is immutable: even if `v7` is repointed later, the workflow still resolves to the exact commit that was reviewed.

**The tradeoff, and why it is not an argument for going back to tags:** a raw SHA never moves, so it never picks up legitimate patches either, real vulnerability fixes included. The answer to that is Dependabot, not a floating tag. A major-tag pin's only advantage IS the silent auto-update, and that is precisely the supply-chain liability.

### Resolving a version to its SHA

Use the commits endpoint. It returns the **commit** SHA for both lightweight and annotated tags:

```bash
gh api repos/<owner>/<repo>/commits/<vX.Y.Z> --jq .sha

# Real example
gh api repos/actions/checkout/commits/v7.0.0 --jq .sha
# 9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0
```

**Do not reach for `git/refs/tags/<tag>` blindly.** It returns whatever object the tag points at, and for an **annotated** tag that object is a tag, not a commit:

```bash
# Lightweight tag: object.type is "commit", the SHA is usable as-is
gh api repos/actions/checkout/git/refs/tags/v7.0.0 --jq '.object.type + " " + .object.sha'
# commit 9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0

# Annotated tag: object.type is "tag", one more dereference is required
gh api repos/pnpm/action-setup/git/refs/tags/v6.0.9 --jq '.object.type + " " + .object.sha'
# tag 008330803749db0355799c700092d9a85fd074e9
gh api repos/pnpm/action-setup/git/tags/008330803749db0355799c700092d9a85fd074e9 --jq .object.sha
# 0ebf47130e4866e96fce0953f49152a61190b271
```

Pasting a tag-object SHA into `uses:` produces a workflow that cannot resolve the action. The one-step `commits/<tag>` form sidesteps the distinction entirely, so prefer it.

**Every SHA written in this file is a point-in-time example**, resolved on **2026-08-23** against the versions in the canonical table, and it goes stale as new releases ship. Re-resolve with the command above each time you pin, and never copy a SHA from one action onto another: a SHA identifies a commit inside one specific repository.

### Dependabot is mandatory, not optional

Without it a SHA pin is frozen forever and the repo silently accumulates unpatched actions. Every repo needs a `.github/dependabot.yml` carrying a `github-actions` entry:

```yaml
version: 2
updates:
  - package-ecosystem: "github-actions"
    directory: "/"
    schedule:
      interval: "weekly"
```

`directory: "/"` is what makes Dependabot scan `.github/workflows/`: for this ecosystem the path is given relative to the repo root, not as the workflows directory itself. This entry is separate from any `npm` entry already present, both sit under the same `updates:` list.

Dependabot understands a SHA pin carrying a `# vX.Y.Z` comment: it opens a PR that bumps the SHA **and** the comment, with the release notes in the body. Updates still arrive, as a reviewable, CI-gated PR instead of a silent swap. Triage them with the **dependabot-triage** skill.

---

## Go

This file owns the numbers. The patterns themselves live in `golang-patterns` (toolchain, `go.mod`, golangci-lint v2), `golang-testing`, `chi-patterns`, and `sqlc-patterns`.

### Canonical versions (August 2026)

| Tool | Standard version | EOL / next change |
|---|---|---|
| **Go toolchain** | `1.27.0` (released 2026-08-18) | The two most recent majors are supported: `1.27.x` and `1.26.x`. `1.26` falls out of support when `1.28` ships. Go ships roughly every 6 months, historically Feb/Aug, so watch ~Feb 2027 |
| `github.com/go-chi/chi/v5` | `v5.3.2` | No v6 and no v2 module: chi has stayed on one major since inception |
| `github.com/sqlc-dev/sqlc` (CLI) | `v1.31.1` | Installed with `go install ...@v1.31.1`, not a `require`: app code never imports the sqlc Go API |
| `github.com/jackc/pgx/v5` | `v5.10.0` | |
| `github.com/pressly/goose/v3` | `v3.27.3` | Requires Go 1.25+ |
| `github.com/golangci/golangci-lint/v2` (CLI) | `v2.13.1` | v2 config format only (`version: "2"` in `.golangci.yml`). A v1 config hard-fails |
| `github.com/google/go-cmp` | `v0.7.0` | |
| `github.com/stretchr/testify` | `v1.12.1` | API frozen at v1 by project policy |
| `go.uber.org/mock` | `v0.6.0` | The maintained fork. `github.com/golang/mock` (Google's original) is archived since 2024 |
| `github.com/matryer/moq` (CLI) | `v0.7.1` | |
| `github.com/testcontainers/testcontainers-go` | `v0.44.0` | Still pre-1.0: pin exactly, a minor bump can break the API |
| `github.com/ory/dockertest/v4` | `v4.0.0` | Stable major since April 2026 |
| `github.com/jackc/pgerrcode` | `v0.0.0-20250907135507-afb5586c32a6` | No tagged release exists: this pseudo-version IS the pin |
| `golang.org/x/sync` (`errgroup`) | `v0.22.0` | |
| `github.com/go-playground/validator/v10` (request validation) | `v10.30.3` | |
| `github.com/go-chi/cors` | `v1.2.2` | |
| `github.com/go-chi/httprate` | `v0.16.0` | |
| `github.com/riverqueue/river` (Postgres-backed job queue) | `v0.44.1` | Still pre-1.0: pin exactly, a minor bump can break the API. The subpackages `riverdriver/riverpgxv5` and `rivermigrate` are tagged in lockstep, pin all three at the same version |
| `github.com/redis/go-redis/v9` (Redis client) | `v9.22.0` | Latest stable (published 2026-08-03). Pre-release tags also appear in the version list (`v9.22.0-beta.1`): pin the stable one, never a beta |

Modules verified with `go list -m -versions <module>` on **2026-08-23** (it prints every tagged release, the last one is the latest); the toolchain against `https://go.dev/dl/?mode=json`. **Pin the exact patch.** Unlike a GitHub Action there is no floating major to ride here: a module version is the whole version. Revalidate with `go list -m -versions <module>` every ~6 months. Go's own toolchain cadence is the natural trigger: a new major lands roughly every 6 months (historically February and August), so recheck the whole table when one ships.

### Rule 1 has no Go counterpart (exact by construction)

Nothing to configure and nothing to convert. `go.mod` `require` versions are already exact: the module system has no `^`/`~` range syntax, so there is no widening to prevent and no `save-exact` equivalent to look for. Bump with `go get module@version`, and commit `go.sum` alongside `go.mod` (checksums, not versions). See `golang-patterns` for the `go mod tidy` / `-mod=readonly` discipline.

### Rule 2 equivalent: pin the language version in `go.mod`

`engines` plus `packageManager` in `package.json` map onto the two directives at the top of `go.mod`:

```
go 1.27              // language version: gates semantics
toolchain go1.27.0   // optional pin of the toolchain that builds it
```

`go 1.27` selects language semantics (the 1.22 loop-variable change is the classic example), not just a minimum install. `toolchain go1.27.0` pins the exact compiler that builds the module, which is what makes a laptop and the CI runner agree. Declare both in every repo. The same number pins the builder image: `FROM golang:1.27-alpine` in the build stage (see `docker-patterns` for the multi-stage layout).

### Auditing an existing Go project

```bash
# 1. Language and toolchain directives
head -3 go.mod

# 2. Installed linter vs the pin (v2.13.1)
golangci-lint version

# 3. Latest tagged release of any dependency in question
go list -m -versions github.com/go-chi/chi/v5
```

A pointer, not a tutorial: the toolchain teaching itself lives in `golang-patterns` under **Toolchain**. Fix any drift as part of the ongoing work.

---

## Auditing an existing project

When touching an existing project, review these points as part of the ongoing work:

```bash
# 1. Node version in workflows
grep -rh "node-version" .github/workflows/

# 2. pnpm version conflict between packageManager and action-setup
grep -rh "version:" .github/workflows/ | grep -i pnpm
cat package.json | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('packageManager',''))"

# 3. Caret or tilde in dependencies
grep -c '"[\^~]' package.json && echo "Non-exact versions found" || echo "OK"

# 4. Playwright: repo version vs canonical (1.60.0) and vs runner Dockerfile (must match)
grep -rh '"@playwright/test"' apps/*/package.json package.json 2>/dev/null
grep -rh 'playwright@' .github/ Dockerfile* 2>/dev/null   # if the browser is baked into the runner image

# 5. Actions still pinned by tag instead of by 40-char commit SHA (Rule 6)
grep -rhoE 'uses: [^@ ]+@[^ ]+' .github/workflows/ | grep -vE '@[0-9a-f]{40}$' || echo "OK: every action is SHA-pinned"

# 6. SHA-pinned but missing the `# vX.Y.Z` comment (dependabot-triage needs it to categorize the PR)
grep -rhoE 'uses: [^@ ]+@[0-9a-f]{40}.*' .github/workflows/ | grep -vE '#[[:space:]]*v[0-9]' || echo "OK: every SHA pin carries its version comment"

# 7. Dependabot covers github-actions (without it, SHA pins never get updated)
grep -q 'package-ecosystem: *"\?github-actions' .github/dependabot.yml 2>/dev/null \
  && echo "OK: github-actions ecosystem configured" \
  || echo "MISSING: add a github-actions entry to .github/dependabot.yml"
```

If discrepancies show up, fix them as part of the ongoing work. Don't create a separate task for it.

---

## Migration to pnpm 11

> **Active recommendation, not blocking.** pnpm 11 was released on April 28, 2026. It brings significant security improvements: hardened supply-chain defaults, a SQLite store instead of a JSON file per package, native ESM. Evaluate migration in Q3 2026 once it has more adoption in the ecosystem.

**Prerequisite:** Node 22+ (satisfied if using Node 24 per the canonical Rule).

**Impact of upgrading from pnpm 10 to pnpm 11:**

| Area | Change | Risk |
|---|---|---|
| Lockfile | Mandatory regeneration | Medium: verify resolved versions don't change |
| `pnpm publish` | No longer delegates to the npm CLI | Low: uses a native implementation |
| Store index | Per-package JSON → SQLite | Low: transparent to the user |
| Configuration | New security defaults (`onlyBuiltDependencies`) | Medium: check whether any native package fails |
| pnpm as a module | Now pure ESM | Only affects consumers importing pnpm as a library |

**Per-repository migration procedure:**

```bash
# 1. Check the exact available version
npm view pnpm@11 version

# 2. Update packageManager in package.json
npm pkg set packageManager="pnpm@11.x.x"   # replace x.x with the exact version

# 3. Regenerate the lockfile
rm pnpm-lock.yaml
pnpm install

# 4. Verify resolved versions didn't change unexpectedly
git diff pnpm-lock.yaml | grep "^+" | grep "version:" | head -30

# 5. Verify native builds still work
pnpm install --frozen-lockfile

# 6. Run the full test suite
pnpm test

# 7. Push to dev and verify CI passes before continuing to staging/prod
```

**Recommended migration order across repos:** start with projects that have fewer native dependencies (postinstall scripts, bindings) before moving to more critical ones like auth-gateway or portfolio-api.
