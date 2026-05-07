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

- `package.json`: resolves the full dependency tree from the npm registry.
- `package-lock.json` / `npm-shrinkwrap.json`: imports the resolved tarball set directly.

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

Typical flow:

```sh
# Optional: rebuild/verify target inventory from the registry.
go run ./cmd/golden-retriever state sync-target \
  --input package.json \
  --state .gr/state.json \
  --metadata-cache .gr/metadata \
  --target-registry "$NPM_TARGET_REGISTRY"

go run ./cmd/golden-retriever fetch \
  --input package.json \
  --state .gr/state.json \
  --metadata-cache .gr/metadata \
  --out .gr/tgzs

# Planned: authenticated parallel push that updates state.target.
go run ./cmd/golden-retriever push \
  --state .gr/state.json \
  --target-registry "$NPM_TARGET_REGISTRY"
```

Authentication should come from CI variables via `.npmrc` or environment expansion, for example an auth token scoped to the target registry.

## Test Strategy

Fast tests use mock npm registry responses and lockfiles. npm parity tests are opt-in because they use real npm and the public registry:

```sh
NPM_PARITY=1 go test ./...
```

The parity tests generate npm lockfiles and compare the package tarball set with this tool.
