# CI workflow maintenance

Small cleanup items surfaced by GitHub Actions warnings during the v1.24.3 release build. Neither is urgent, both are cheap.

## 1. Node 20 → Node 24 action version bump

**What:** GitHub Actions started warning that `actions/checkout@v4`, `actions/setup-go@v5`, and `actions/upload-artifact@v4` still run on Node 20, which is being retired.

**Warning text (from v1.24.3 build):**

> Node.js 20 actions are deprecated. The following actions are running on Node.js 20 and may not work as expected: actions/checkout@v4, actions/setup-go@v5, actions/upload-artifact@v4. Actions will be forced to run with Node.js 24 by default starting June 2nd, 2026. Node.js 20 will be removed from the runner on September 16th, 2026.

**Deadline:** **September 16, 2026** — after that, Node 20 actions stop working.

**How:**

- Check the latest major version of each action on GitHub Marketplace (likely `actions/checkout@v5`, `actions/setup-go@v6`, `actions/upload-artifact@v5` by then)
- Update every `uses:` line in `.github/workflows/*.yml`
- Test a dummy commit to confirm the pipeline still passes

Or, as a temporary escape hatch, set `FORCE_JAVASCRIPT_ACTIONS_TO_NODE24=true` on the runner / in the workflow — this opts in to Node 24 with the existing action versions. Useful if the maintainers haven't tagged a Node 24 release yet by the deadline.

**Cost:** 10-15 minutes + one CI roundtrip.

## 2. `go.sum` cache warning in `actions/setup-go`

**What:** `actions/setup-go@v5` tries to cache Go modules and fails with:

> Restore cache failed: Dependencies file is not found in /home/runner/work/proxyness/proxyness. Supported file pattern: go.sum

**Why it happens:** The repo is a Go workspace (`go.work` at the root) with per-module `go.sum` files under `daemon/go.sum`, `server/go.sum`, `pkg/go.sum`, etc. `setup-go` looks for a single `go.sum` at the working directory by default and finds nothing.

**Effect:** Just a warning — the build succeeds because modules get downloaded fresh every run. Downside is slower builds (no cached module downloads) and a noisy log.

**How:**

Pass `cache-dependency-path` to `setup-go`:

```yaml
- uses: actions/setup-go@v5
  with:
    go-version: "1.26"
    cache-dependency-path: |
      daemon/go.sum
      server/go.sum
      pkg/go.sum
      test/go.sum
      helper/go.sum
```

That tells the cache which files to use as the cache key.

**Cost:** 5 minutes + one CI roundtrip to verify the cache hits on the second build.

## When to do this

Do them together — both are single-file edits to `.github/workflows/*.yml` and can be bundled with the next CI cleanup commit. Flag them with `[skip-deploy]` in the commit message so the production server doesn't restart for workflow-only changes.
