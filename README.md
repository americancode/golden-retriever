# golden-retriever

`golden-retriever` is a Go CLI for collecting npm package tarballs for air-gapped environments.

The target behavior is npm-compatible resolution using the npm CLI 11.14.0 source in `cli-11.14.0` as the local reference, while optimizing the acquisition path: concurrent registry metadata reads, concurrent tarball downloads, integrity verification, and resumable state.

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

## Test Strategy

Fast tests use mock npm registry responses and lockfiles. npm parity tests are opt-in because they use real npm and the public registry:

```sh
NPM_PARITY=1 go test ./...
```

The parity tests generate npm lockfiles and compare the package tarball set with this tool.
