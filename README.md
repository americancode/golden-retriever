# golden-retriever

Repository: https://github.com/americancode/golden-retriever

`golden-retriever` mirrors npm package tarballs for air-gapped environments. It resolves npm inputs, downloads the required `.tgz` files, optionally scans them for known vulnerabilities, and publishes missing package versions to a target npm-compatible registry.

The normal production flow is:

```text
resolve inputs -> fetch missing tarballs -> scan/audit -> push missing versions -> update target state
```

State is central to the design. `.gr/state.json` records what the target registry already contains so later runs can skip package versions that are already present without needing to re-download or re-publish them.

## Current Resolution Model

`golden-retriever` intentionally uses npm to produce npm-compatible lockfiles for `package.json` inputs, then uses those lockfiles as the source of truth for tarball acquisition.

- `package.json`: runs npm lockfile generation with scripts, audit, funding, and progress disabled, then imports the generated lockfile.
- `package-lock.json` / `npm-shrinkwrap.json`: imports the lockfile directly and downloads the registry tarballs it references.
- Multiple inputs can be processed in parallel with `--inputs` and `--project-concurrency`.
- Duplicate package versions across projects are deduped before fetch and push.
- Lockfile inputs are treated as authoritative. They are not re-resolved or platform-filtered by `golden-retriever`.

For `package.json` inputs, `--npm-platforms` can run npm resolution once per target platform and union the results. This is useful for mirroring native optional packages for multiple deployment targets:

```sh
golden-retriever mirror \
  --input package.json \
  --npm-platforms "linux/x64/glibc,linux/arm64/glibc,darwin/arm64,win32/x64" \
  --target-registry "$NPM_TARGET_REGISTRY"
```

During multi-platform resolution, logs include one resolve start/done pair per platform, for example:

```text
resolve:npm-lock:start input=package.json platform=linux/x64/glibc
resolve:npm-lock:done input=package.json platform=linux/x64/glibc packages=312 unique=312
```

## Quick Start

Build locally:

```sh
go build ./cmd/golden-retriever
```

Mirror one project to a target registry:

```sh
./golden-retriever mirror \
  --input ./package.json \
  --state ./.gr/state.json \
  --metadata-cache ./.gr/metadata \
  --out ./.gr/tgzs \
  --target-registry "$NPM_TARGET_REGISTRY"
```

Mirror multiple projects in one run:

```sh
./golden-retriever mirror \
  --inputs "./apps/react/package.json,./apps/angular/package-lock.json" \
  --project-concurrency 4 \
  --state ./.gr/state.json \
  --metadata-cache ./.gr/metadata \
  --out ./.gr/tgzs \
  --target-registry "$NPM_TARGET_REGISTRY"
```

Fetch tarballs without publishing:

```sh
./golden-retriever fetch \
  --input ./package-lock.json \
  --out ./.gr/tgzs \
  --state ./.gr/state.json
```

Scan target inventory without local tarballs:

```sh
./golden-retriever state sync-target \
  --state ./.gr/state.json \
  --target-registry "$NPM_TARGET_REGISTRY"

./golden-retriever scan \
  --state ./.gr/state.json \
  --source target \
  --provider osv-offline \
  --osv-offline-db /var/lib/osv-scanner/db \
  --report ./.gr/scan-report.json
```

## Inputs

Supported direct inputs:

- `package.json`
- `package-lock.json`
- `npm-shrinkwrap.json`

Single input:

```sh
golden-retriever mirror --input package.json ...
```

Multiple inputs:

```sh
golden-retriever mirror --inputs "ui-a/package.json,ui-b/package-lock.json" ...
```

The GitLab CI template also discovers inputs from directories. By default it scans `package-jsons/` and `package-locks/` for:

- `package.json`
- `package-lock.json`
- `npm-shrinkwrap.json`
- `*.package.json`
- `*.package-lock.json`

Custom names like `myapp.package.json` and `myapp.package-lock.json` are supported by that CI discovery step.

## State and Cache

Common paths:

- `.gr/state.json`: local target inventory state.
- `.gr/metadata/`: npm source registry metadata cache.
- `.gr/tgzs/`: local tarball staging directory.
- `.gr/scan-report.json`: scan/audit report artifact.

The state file is not just a resume file. It is the local record of target registry contents. If `state.target` says a package version is already present in the target registry, `golden-retriever` can skip fetching and pushing that version.

Refresh state from the target registry:

```sh
golden-retriever state sync-target \
  --state .gr/state.json \
  --target-registry "$NPM_TARGET_REGISTRY"
```

Normal CI runs should usually rely on the cached state file. Use `state sync-target` when rebuilding or verifying inventory against the target registry.

## Authentication

Target registry auth is non-interactive and CI-friendly. When `--target-registry` is set, the target client can use npmrc auth and these environment variables.

Token precedence:

```text
NPM_TARGET_TOKEN
NPM_AUTH_TOKEN
NODE_AUTH_TOKEN
NPM_TOKEN
CI_JOB_TOKEN
```

Username/password precedence:

```text
NPM_TARGET_USERNAME + NPM_TARGET_PASSWORD
CI_DEPLOY_USER + CI_DEPLOY_PASSWORD
NPM_USERNAME + NPM_PASSWORD
```

For GitLab Package Registry, `CI_JOB_TOKEN` can be used when the project/group permissions allow package publish.

TLS verification can be disabled for a target registry when needed:

```sh
golden-retriever mirror ... --target-insecure-skip-verify
```

Prefer installing the correct CA certificate in the runner/image instead of disabling TLS verification.

## Scanning

`mirror` runs scanning by default before publish:

```text
--scan-auto=true
--scan-osv=true
--scan-enforce=false
```

The default mode is audit. Findings are printed and written to the report, but packages still publish unless enforcement is enabled.

Turn enforcement on:

```sh
golden-retriever mirror ... --scan-enforce=true
```

Disable scanning entirely:

```sh
golden-retriever mirror ... --scan-auto=false --scan-osv=false
```

### OSV Providers

Supported providers:

- `osv-api`: direct OSV API queries. If the API fails, the scanner falls back once to local offline OSV scanning.
- `osv-offline`: local `osv-scanner` only. No OSV API calls are made.

Mirror flags:

```sh
--scan-provider osv-offline
--scan-osv-offline-db /var/lib/osv-scanner/db
--scan-osv-api-batch-size 200
--scan-osv-api-concurrency 8
--scan-osv-offline-chunk-size 50
--scan-osv-offline-concurrency 4
--scan-osv-offline-retry-failed-chunks=true
```

Standalone `scan` flags use the same names without the `scan-` prefix:

```sh
--provider osv-offline
--osv-offline-db /var/lib/osv-scanner/db
--osv-api-batch-size 200
--osv-api-concurrency 8
--osv-offline-chunk-size 50
--osv-offline-concurrency 4
--osv-offline-retry-failed-chunks=true
```

The CI image includes `osv-scanner` and a prewarmed offline npm vulnerability database at `/var/lib/osv-scanner/db`.

### Severity Thresholds

Block packages at or above a severity threshold:

```sh
golden-retriever mirror ... \
  --scan-enforce=true \
  --scan-min-severity high \
  --scan-unknown-severity high
```

Valid severity values:

```text
low
medium
high
critical
```

Findings include vulnerability IDs, severity, and URLs in stdout and `.gr/scan-report.json`.

### Manual Blocklist

Manual blocks are file-based:

```sh
golden-retriever mirror ... --scan-blocklist .gr/scan-blocklist.json
golden-retriever scan ... --blocklist .gr/scan-blocklist.json
```

Example:

```json
{
  "packages": ["lodash", "@blocked/some-lib"],
  "packageVersions": ["minimist@0.0.8", "lodash@4.17.20"],
  "packagePrefixes": ["@blocked/"]
}
```

If the blocklist file is not present, it is ignored.

### Exceptions

Exceptions allow known findings to pass the severity gate:

```sh
golden-retriever mirror ... --scan-exceptions .gr/scan-exceptions.json
golden-retriever scan ... --exceptions .gr/scan-exceptions.json
```

Example:

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

`package` can be a package name or `name@version`. Expired exceptions are ignored.

## GitLab CI

This repository includes `.gitlab-ci.yml` for the expected production flow.

Jobs:

- `mirror:npm`: discovers inputs, resolves/fetches/scans/pushes, updates cached state.
- `state:rebuild`: manual job that rebuilds `.gr/state.json` from the target registry.
- `rescan:target`: manual or scheduled job that rebuilds state in the job and scans target inventory.

The jobs share:

```yaml
resource_group: "golden-retriever-state-${CI_PROJECT_ID}-${CI_COMMIT_REF_SLUG}"
```

That prevents state-mutating jobs from running concurrently on the same branch cache.

The default cache key is branch/project scoped:

```yaml
key: "gr-${CI_PROJECT_ID}-${CI_COMMIT_REF_SLUG}"
paths:
  - .gr/state.json
  - .gr/metadata/
```

The cache does not store local tarballs by default. The point of state is to know what the destination registry already has, not to carry downloaded `.tgz` files between jobs.

Important variables:

```yaml
NPM_TARGET_REGISTRY: "https://gitlab.example.com/api/v4/projects/123/packages/npm/"
GOLDEN_RETRIEVER_CI_IMAGE: "ghcr.io/americancode/golden-retriever-ci:main"
GOLDEN_RETRIEVER_INPUT_DIRS: "package-jsons,package-locks"
GOLDEN_RETRIEVER_NPM_PLATFORMS: "linux/x64/glibc,linux/arm64/glibc"
GOLDEN_RETRIEVER_PROJECT_CONCURRENCY: "4"
GOLDEN_RETRIEVER_SCAN_PROVIDER: "osv-offline"
GOLDEN_RETRIEVER_SCAN_ENFORCE: "false"
GOLDEN_RETRIEVER_SCAN_MIN_SEVERITY: "high"
```

For audit-only scanning, keep:

```yaml
GOLDEN_RETRIEVER_SCAN_ENFORCE: "false"
```

To block publish on policy failures:

```yaml
GOLDEN_RETRIEVER_SCAN_ENFORCE: "true"
```

## CI Image

The CI image is built from `Dockerfile.ci` and published by `.github/workflows/docker-publish-ci-image.yml`.

Default image:

```text
ghcr.io/americancode/golden-retriever-ci:main
```

The image includes:

- `golden-retriever`
- Node.js
- latest npm
- `ca-certificates`
- `osv-scanner`
- prewarmed offline OSV database at `/var/lib/osv-scanner/db`

The GitHub Actions workflow:

- runs Trivy filesystem and image scans
- builds the CI image from `Dockerfile.ci`
- publishes to GHCR on `main`, tags, and manual dispatch
- signs pushed images with cosign on non-PR runs

Build locally:

```sh
podman build -f Dockerfile.ci -t golden-retriever-ci:local .
```

Verify a pushed image signature:

```sh
cosign verify ghcr.io/americancode/golden-retriever-ci:main \
  --certificate-identity-regexp 'https://github.com/americancode/golden-retriever/.github/workflows/docker-publish-ci-image.yml@refs/heads/main' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Proxy and CA Configuration

The Go HTTP client and npm subprocess honor standard proxy variables:

```sh
export HTTP_PROXY=http://proxy.corp.local:3128
export HTTPS_PROXY=http://proxy.corp.local:3128
export NO_PROXY=127.0.0.1,localhost,.svc,.cluster.local,gitlab.example.com,registry.internal.example
```

Use lowercase variants too if your environment requires them:

```sh
export http_proxy="$HTTP_PROXY"
export https_proxy="$HTTPS_PROXY"
export no_proxy="$NO_PROXY"
```

If your proxy or internal registry uses a private CA, install the CA into the runner/container trust store before running `golden-retriever`. The CI image includes `ca-certificates` and `update-ca-certificates`.

## Logging

Default logs show high-level progress for resolve, fetch, scan, and push. Use `--trace` for detailed diagnostics.

Useful trace cases:

- npm lockfile generation per input/platform
- OSV API calls and offline scanner batches
- target registry auth selection
- target registry publish attempts
- state rebuild progress

Example:

```sh
golden-retriever mirror ... --trace
```

## Commands

```text
fetch     resolve and download every package tarball
mirror    resolve, optionally sync target state, fetch tarballs, scan, and push missing packages
push      publish local tarballs missing from target registry
scan      evaluate local or target inventory and persist scan status in state
resolve   print the resolved package tarball set
state     manage target registry inventory state
cache     manage metadata cache
```

Use command help for full flag lists:

```sh
golden-retriever mirror -h
golden-retriever scan -h
golden-retriever state sync-target -h
```

## Development

Run tests:

```sh
go test ./...
```

Build:

```sh
go build ./cmd/golden-retriever
```

Run the CLI from source:

```sh
go run ./cmd/golden-retriever --help
```
