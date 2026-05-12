# golden-retriever

`golden-retriever` is a Go CLI for collecting npm package tarballs for air-gapped environments and publishing them into a target npm-compatible registry.

The target behavior is npm-compatible resolution using the npm CLI 11.14.0 source in `cli-11.14.0` as the local reference. After this tool resolves and fetches the required tarballs, it is also responsible for pushing them to the target registry. npm should then be able to install the original `package.json` successfully when configured to use that target registry.

## Usage

```sh
go run ./cmd/golden-retriever fetch \
  --input ./package.json \
  --out ./tgzs \
  --state ./.gr/state.json \
  --metadata-cache ./.gr/metadata \
  --metadata-cache-ttl 24h \
  --concurrency 32
```

Mirror (resolve + deduped fetch + scan + push):

```sh
go run ./cmd/golden-retriever mirror \
  --input ./package.json \
  --inputs "./apps/react/package.json,./apps/angular/package-lock.json" \
  --project-concurrency 4 \
  --out ./.gr/tgzs \
  --state ./.gr/state.json \
  --metadata-cache ./.gr/metadata \
  --target-registry https://registry.internal.example
```

Supported inputs:

- `package.json`: resolves the full dependency tree from the npm registry. Basic workspaces are supported by discovering workspace package globs, skipping local workspace tarball acquisition, and resolving each workspace package's external dependencies.
- `package-lock.json` / `npm-shrinkwrap.json`: imports the resolved tarball set directly and skips bundled/link entries that are not separate registry tarballs.

The output directory receives tarballs named as `<escaped-name>-<version>.tgz`; scoped packages are escaped as `@scope+pkg`.

When `--inputs` is used, project resolution runs in parallel, then package sets are globally deduped before fetch/push so duplicate package versions are downloaded/published once per run.

Registry metadata is cached on disk by default under `.gr/metadata`. Fresh entries are used directly; stale entries are revalidated with `ETag` / `Last-Modified` headers. The CLI reads `~/.npmrc`, a project `.npmrc` next to the input file, and an optional extra file from `--npmrc`; it supports default registries, scoped registries, and common registry auth keys.

The state file is target registry inventory first, local download cache second. It is intended to be maintained locally and reused from CI cache, for example GitLab cache. Packages marked as present in the target registry are skipped by `fetch` even if no local tarball exists. Querying the target registry should be optional and used to rebuild or verify state when needed:

```sh
go run ./cmd/golden-retriever state sync-target \
  --input ./package.json \
  --state ./.gr/state.json \
  --target-registry https://registry.internal.example

go run ./cmd/golden-retriever state mark-target \
  --state ./.gr/state.json \
  --package left-pad@1.3.0 \
  --integrity sha512-...
```

## Scanning and Vulnerability Controls

`mirror` includes a scan step by default (`--scan-auto=true`) and writes a report (`--scan-report .gr/scan-report.json`).

Default mode is **audit**:

- `--scan-enforce=false` (default): findings are reported but publish proceeds.
- `--scan-enforce=true`: failing findings block publish.

### Manual blocks

Use blocklist file:

- mirror: `--scan-blocklist .gr/scan-blocklist.json`
- scan: `--blocklist .gr/scan-blocklist.json`

Example blocklist file:

```json
{
  "packages": ["lodash", "@blocked/some-lib"],
  "packageVersions": ["minimist@0.0.8", "lodash@4.17.20"],
  "packagePrefixes": ["@blocked/"],
}
```

### OSV-based CVE checks

- enable/disable OSV lookup: `--scan-osv=true|false`
- provider selection: `--scan-provider osv-api|osv-offline`
- OSV API batch size: `--scan-osv-batch-size 200`
- offline `osv-scanner` chunk size: `--scan-osv-offline-chunk-size 100`
- parallel vulnerability detail lookups: `--scan-osv-concurrency 8`
- fail threshold: `--scan-min-severity high`
- fallback when severity missing: `--scan-unknown-severity high`
- exceptions file: `--scan-exceptions .gr/scan-exceptions.json`
- offline DB path: `--scan-osv-offline-db /var/lib/osv-scanner/db`

Provider behavior:

- `osv-api` (default): use direct OSV API queries first; if that fails, automatically fall back to local `osv-scanner` offline vulnerability matching.
- `osv-offline`: skip direct API calls and use local `osv-scanner` offline vulnerability matching only.

`osv-scanner` offline mode uses a local vulnerability database. `golden-retriever` passes `OSV_SCANNER_LOCAL_DB_CACHE_DIRECTORY` through to the tool when set.

The CI image also includes a prewarmed offline OSV database at `/var/lib/osv-scanner/db` and sets:

```sh
OSV_SCANNER_LOCAL_DB_CACHE_DIRECTORY=/var/lib/osv-scanner/db
```

So `osv-offline` works immediately even before CI cache is populated. If GitLab CI sets `GOLDEN_RETRIEVER_SCAN_OSV_OFFLINE_DB`, that runtime path overrides the image default.

Enforcement behavior:

- With `--scan-enforce=true`, packages with OSV findings at or above `--scan-min-severity` are blocked from publish.
- `--scan-exceptions` is applied before blocking, so matching exceptions can allow findings that would otherwise fail the threshold.
- `expiresAt` on exceptions is enforced; expired exceptions no longer bypass threshold blocking.

Example exceptions file:

```json
{
  "exceptions": [
    {
      "package": "lodash@4.17.20",
      "vulnId": "GHSA-xxxx-xxxx-xxxx",
      "expiresAt": "2026-12-31T00:00:00Z",
      "reason": "temporary exception"
    }
  ]
}
```

### Disable all scanning/audit

To fully disable scanning in `mirror`:

```sh
go run ./cmd/golden-retriever mirror ... \
  --scan-auto=false \
  --scan-osv=false
```

`scan` command can be run independently:

```sh
go run ./cmd/golden-retriever scan \
  --state .gr/state.json \
  --source local \
  --osv=true \
  --provider osv-api \
  --osv-offline-db /var/lib/osv-scanner/db \
  --report .gr/scan-report.json
```

`--source` values:
- `local`: local tarball inventory
- `target`: target registry inventory from `state.target` (no local tgzs required)
- `both`

Target registry pushes must be authenticated and should run in parallel wherever the registry can tolerate it. After a successful push, the program should update `state.target` so future runs avoid re-fetching and re-pushing that package version.

## CI Image Build (GitHub Actions)

This repository includes `.github/workflows/docker-publish-ci-image.yml` to build and publish the CI image from `Dockerfile.ci` to GHCR.

- image name: `ghcr.io/<owner>/<repo>-ci`
- push on `main`, tags, and manual dispatch
- PRs build without push
- uses Buildx cache and cosign signing (non-PR)

`Dockerfile.ci` uses pinned base and installs Go + Node + npm latest:

- base: `golang:1.24-alpine3.22`
- installs `nodejs`, `npm`, and upgrades npm globally

## GitLab CI

The expected production trigger is a GitLab CI pipeline. The state file and metadata cache should be stored in GitLab cache so normal runs avoid querying the target registry unless explicitly requested.

This repository includes a baseline `.gitlab-ci.yml` that:

- uses your custom CI image (`$GOLDEN_RETRIEVER_CI_IMAGE`)
- discovers inputs from `package-jsons/` and `package-locks/`
- runs one program-centric pipeline (`mirror`) with internal parallelism
- serializes `mirror`, `state:rebuild`, and `rescan:target` with a shared `resource_group` so branch state is not mutated concurrently
- caches:
  - `.gr/state.json`
  - `.gr/metadata/`
- publishes scan report artifact (`.gr/scan-report.json`)

Typical flow:

```sh
go run ./cmd/golden-retriever mirror \
  --input package.json \
  --state .gr/state.json \
  --metadata-cache .gr/metadata \
  --out .gr/tgzs \
  --target-registry "$NPM_TARGET_REGISTRY" \
  --sync-target
```

### Key GitLab variables

- `NPM_TARGET_REGISTRY`: target npm-compatible registry (required)
- `GOLDEN_RETRIEVER_INPUT`: fallback single input (default `package.json`)
- `GOLDEN_RETRIEVER_INPUT_DIRS`: discovery dirs (default `package-jsons,package-locks`)
- `GOLDEN_RETRIEVER_PROJECT_CONCURRENCY`: cross-project resolution parallelism
- `GOLDEN_RETRIEVER_SCAN_ENFORCE`: `false` (audit) or `true` (block)
- `GOLDEN_RETRIEVER_SCAN_OSV`: `true|false`
- `GOLDEN_RETRIEVER_SCAN_PROVIDER`: `osv-api|osv-offline`
- `GOLDEN_RETRIEVER_SCAN_OSV_OFFLINE_DB`: local OSV scanner DB path
- `GOLDEN_RETRIEVER_SCAN_OSV_BATCH_SIZE`: OSV API batch size
- `GOLDEN_RETRIEVER_SCAN_OSV_OFFLINE_CHUNK_SIZE`: offline `osv-scanner` chunk size
- `GOLDEN_RETRIEVER_SCAN_OSV_CONCURRENCY`: vulnerability scan parallelism
- `GOLDEN_RETRIEVER_SCAN_MIN_SEVERITY`: `low|medium|high|critical`
- `GOLDEN_RETRIEVER_SCAN_EXCEPTIONS`: exceptions file path
- `GOLDEN_RETRIEVER_SCAN_BLOCKLIST`: blocklist file path
- `GOLDEN_RETRIEVER_SCAN_REPORT`: report path for artifacts

### Proxy configuration (HTTP/HTTPS/NO_PROXY)

`golden-retriever` and the npm subprocess it uses for `package.json` lockfile resolution both honor standard proxy environment variables:

- `HTTP_PROXY` / `http_proxy`
- `HTTPS_PROXY` / `https_proxy`
- `NO_PROXY` / `no_proxy`

Example:

```sh
export HTTPS_PROXY=http://proxy.corp.local:3128
export HTTP_PROXY=http://proxy.corp.local:3128
export NO_PROXY=127.0.0.1,localhost,.svc,.cluster.local,gitlab.example.com,registry.internal.example
```

Notes:

- Include internal registry and GitLab hosts in `NO_PROXY` so internal traffic does not traverse the proxy.
- If your proxy performs TLS interception, ensure runner/container trust store includes your corporate CA.

Authentication should come from CI variables. The CLI supports npmrc auth with environment expansion, and it also reads target registry credentials directly from environment variables when `--target-registry` is set. Token precedence is `NPM_TARGET_TOKEN`, `NPM_AUTH_TOKEN`, `NODE_AUTH_TOKEN`, `NPM_TOKEN`, then `CI_JOB_TOKEN`. Username/password precedence is `NPM_TARGET_USERNAME` + `NPM_TARGET_PASSWORD`, `CI_DEPLOY_USER` + `CI_DEPLOY_PASSWORD`, then `NPM_USERNAME` + `NPM_PASSWORD`.

`mirror` is the CI-oriented command: it resolves the input once, optionally refreshes target-present state with `--sync-target`, fetches only tarballs still needed locally, publishes missing package versions to the target registry in parallel, and updates `state.target` after successful publishes. For fully cached normal runs, omit `--sync-target` and rely on the cached `.gr/state.json`.

### Typical CI job flow

Use the jobs in this order:

1. `mirror:npm`
   - normal branch pipeline job
   - resolves project inputs, fetches tarballs, optionally scans, publishes missing packages
   - writes the canonical cached `.gr/state.json`

2. `state:rebuild`
   - manual maintenance job
   - rebuilds `state.target` from everything the target registry currently contains
   - use this when cache is stale, lost, or you want target inventory to become canonical again
   - this job is allowed to refresh the shared cache

3. `rescan:target`
   - manual or scheduled maintenance job
   - rebuilds an in-job copy of `state.target` from the target registry, then scans `--source target`
   - uploads `.gr/state.json` and `.gr/scan-report.json` as artifacts
   - does **not** push state back into the shared cache

The important distinction is:

- `state:rebuild` is the canonical cache writer for maintenance
- `rescan:target` is read-only from the cache perspective

That prevents scan jobs from overwriting the branch cache with analysis-only state.

### Periodic target re-scan (new CVEs after publish)

Use `rescan:target` job (manual or scheduled) to catch newly disclosed CVEs in target registry packages even after local cache loss:

1. `state sync-target` rebuilds inventory from target registry
2. `scan --source target` checks CVEs using OSV
3. uploads `.gr/scan-report.json` and `.gr/state.json` artifacts

`rescan:target` intentionally rebuilds target inventory again inside the job so it can run independently of `state:rebuild`, but the CI example keeps it from corrupting cached state by using cache `policy: pull` only.

If outbound OSV API access is blocked, keep `GOLDEN_RETRIEVER_SCAN_PROVIDER=osv-api` and ensure the offline DB at `GOLDEN_RETRIEVER_SCAN_OSV_OFFLINE_DB` exists; scan will fall back automatically. If you explicitly set `osv-offline`, no OSV API calls are attempted.

## State and Cache Strategy

- `state.target`: what target registry already has; drives fetch/push skipping
- `state.local`: local tarball records and scan metadata
- `.gr/metadata`: packument metadata cache for fast resolution
- `/var/lib/osv-scanner/db`: baked-in `osv-scanner` offline vulnerability database in the CI image

Recommended:

- keep `.gr/state.json` + `.gr/metadata` in GitLab cache
- let `mirror:npm` and `state:rebuild` be the only jobs that refresh shared state cache
- keep `rescan:target` read-only against cache and use its artifacts for review
- do not cache `.gr/tgzs` unless you specifically need retry support on failed pushes
- use branch-scoped cache keys for stable incremental performance

## Test Strategy

Fast tests use mock npm registry responses and lockfiles. npm parity tests are opt-in because they use real npm and the public registry:

```sh
NPM_PARITY=1 go test ./...
```

The parity tests generate npm lockfiles and compare the package tarball set with this tool.

## Current override spec policy

For npm `overrides`, this tool currently supports registry-like replacement specs (versions, ranges, and dist-tags) and intentionally rejects non-registry override specs. Rejected override selectors/values include:

- alias override specs (`npm:...`)
- directory/local path specs (`../...`, `./...`, absolute paths)
- `file:` / `link:` specs
- git/hosted git specs (`github:`, `git+ssh:`, `git@...`, etc.)

This keeps override behavior deterministic for registry mirroring. Support for those override classes can be added later as an explicit compatibility expansion.
