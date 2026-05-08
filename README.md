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

Supported inputs:

- `package.json`: resolves the full dependency tree from the npm registry. Basic workspaces are supported by discovering workspace package globs, skipping local workspace tarball acquisition, and resolving each workspace package's external dependencies.
- `package-lock.json` / `npm-shrinkwrap.json`: imports the resolved tarball set directly and skips bundled/link entries that are not separate registry tarballs.

The output directory receives tarballs named as `<escaped-name>-<version>.tgz`; scoped packages are escaped as `@scope+pkg`.

Registry metadata is cached on disk by default under `.gr/metadata`. Fresh entries are used directly; stale entries are revalidated with `ETag` / `Last-Modified` headers. Use `--offline` to resolve `package.json` inputs only from that cache. The CLI reads `~/.npmrc`, a project `.npmrc` next to the input file, and an optional extra file from `--npmrc`; it supports default registries, scoped registries, and common registry auth keys.

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

Target registry pushes must be authenticated and should run in parallel wherever the registry can tolerate it. After a successful push, the program should update `state.target` so future runs avoid re-fetching and re-pushing that package version.

## GitLab CI

The expected production trigger is a GitLab CI pipeline. The state file and metadata cache should be stored in GitLab cache so normal runs avoid querying the target registry unless explicitly requested.

This repository includes a baseline `.gitlab-ci.yml` that caches `.gr/state.json`, `.gr/metadata/`, and `.gr/tgzs/`, then runs `golden-retriever mirror` when `NPM_TARGET_REGISTRY` is set. The tarball cache is optional but useful when jobs retry or when target pushes fail after successful fetches.

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

Authentication should come from CI variables. The CLI supports npmrc auth with environment expansion, and it also reads target registry credentials directly from environment variables when `--target-registry` is set. Token precedence is `NPM_TARGET_TOKEN`, `NPM_AUTH_TOKEN`, `NODE_AUTH_TOKEN`, `NPM_TOKEN`, then `CI_JOB_TOKEN`. Username/password precedence is `NPM_TARGET_USERNAME` + `NPM_TARGET_PASSWORD`, `CI_DEPLOY_USER` + `CI_DEPLOY_PASSWORD`, then `NPM_USERNAME` + `NPM_PASSWORD`.

`mirror` is the CI-oriented command: it resolves the input once, optionally refreshes target-present state with `--sync-target`, fetches only tarballs still needed locally, publishes missing package versions to the target registry in parallel, and updates `state.target` after successful publishes. For fully cached normal runs, omit `--sync-target` and rely on the cached `.gr/state.json`.

## Test Strategy

Fast tests use mock npm registry responses and lockfiles. npm parity tests are opt-in because they use real npm and the public registry:

```sh
NPM_PARITY=1 go test ./...
```

The parity tests generate npm lockfiles and compare the package tarball set with this tool.
