# Roadmap

## Goal

Build a Go CLI that acquires every npm package tarball needed for an air-gapped install. The tool must accept `package.json`, `package-lock.json`, and `npm-shrinkwrap.json`, resolve or import the entire dependency tree, and download all required `.tgz` files into a target directory.

The npm CLI 11.14.0 source in `cli-11.14.0` is the local behavioral reference. The Go implementation should reproduce npm's resolution behavior natively while making package acquisition faster and more resumable than npm's normal install-oriented path.

## Current State

- Git repository initialized.
- Go module created as `golden-retriever`.
- CLI entrypoint exists at `cmd/golden-retriever`.
- `fetch` command resolves/imports a package set and downloads tarballs.
- `resolve` command prints the resolved package tarball set.
- Lockfile v2/v3 `packages` import works for `package-lock.json` and `npm-shrinkwrap.json`.
- `package.json` registry dependency walking works for basic registry semver specs.
- npm alias specs like `npm:pkg@range` are supported for tarball acquisition.
- Range support now covers more npm-style cases, including partial versions, hyphen ranges, prerelease ordering, and deprecated-version avoidance.
- Registry packument requests are coalesced so concurrent requests for the same package share one in-flight fetch.
- Dependency resolution now resolves independent child dependencies concurrently.
- Resolved graphs now retain root placement, dependency edges, edge types, and peer dependency edges in addition to the flat tarball set.
- Non-optional peer dependencies can be auto-placed at the parent location when no ancestor satisfies them.
- Optional peer dependencies are recorded without auto-installing when unsatisfied.
- Packument metadata can be cached on disk with `--metadata-cache`.
- Cached packuments support freshness control with `--metadata-cache-ttl`.
- Stale cached packuments revalidate with `If-None-Match` / `If-Modified-Since`, and `304 Not Modified` refreshes cache timestamps.
- `--offline` resolves package inputs using only cached registry metadata.
- `.npmrc` loading supports default registry, scoped registries, bearer tokens, `_auth`, and username/password auth.
- Concurrent tarball downloads are implemented.
- Transient tarball failures retry with backoff.
- Transient packument metadata failures retry with backoff via `--metadata-retries`.
- Stale cached packuments are used on transient metadata failures when available.
- Tarball downloads apply matching `.npmrc` registry auth.
- A JSON state file tracks downloaded packages for resume/skipping.
- Tarball integrity verification streams data and supports `sha512` SRI, `sha1` SRI, and legacy `sha1` shasum.
- Tests cover semver basics, lockfile import, mock registry resolution/fetching, state reuse, and opt-in npm parity.

## Required Outcomes From Initial Prompt

- Accept `package.json` as input.
- Accept npm lockfiles as input.
- Pull npm package tarballs into a target directory.
- Build the whole dependency tree, including sub-dependencies, not only root dependencies.
- Ensure every package pulled matches what npm would pull.
- Build a test framework using npm for parity checks.
- Make package acquisition very efficient.
- Download as much as possible in parallel.
- Maintain a persistent state file tracking what has and has not been downloaded.
- Reference npm CLI source code as the guide.
- Implement the resolver in Go rather than shelling out to npm.
- Optimize for acquiring all `.tgz` files, not for creating `node_modules`.

## Resolution Parity Work

- Port npm package spec parsing behavior from `npm-package-arg`.
- Port npm manifest selection behavior from `npm-pick-manifest`.
- Replace the current minimal semver implementation with npm-compatible semver behavior.
- Expand alias handling beyond registry aliases if npm requires it.
- Continue hardening dist-tags, exact versions, ranges, prereleases, wildcards, hyphen ranges, and OR ranges against npm parity fixtures.
- Support `overrides`.
- Continue hardening `peerDependencies`.
- Continue hardening `peerDependenciesMeta.optional`.
- Reproduce npm Arborist peer conflict behavior, peer set grouping, and strict/legacy peer modes.
- Support `optionalDependencies` with platform and failure semantics matching npm.
- Support `bundleDependencies` / `bundledDependencies`.
- Support `devDependencies` inclusion/exclusion modes.
- Support `peerDependencies` inclusion/exclusion modes where npm behavior requires it.
- Support `workspaces`.
- Support `workspace:` specs if needed for package tree inputs.
- Support `file:`, `link:`, tarball URL, Git, GitHub, and hosted Git specs, or explicitly report unsupported specs with parity tests.
- Support platform filters from `os`, `cpu`, and `libc`.
- Support `engines` handling according to npm config behavior.
- Support deprecation metadata handling where npm uses it for selection or warnings.
- Support lockfile v1 dependency import fully.
- Support npm shrinkwrap behavior differences where relevant.
- Model npm config that affects resolution, including registry, scope registries, strict peer deps, legacy peer deps, omit/include, prefer-dedupe, and install strategy.

## Acquisition Performance Work

- Keep concurrent tarball downloading.
- Continue improving concurrent packument fetching during dependency resolution.
- Add cache pruning and explicit invalidation commands.
- Continue hardening ETag / `If-None-Match` support for packuments.
- Continue hardening retry with exponential backoff for transient packument failures.
- Add rate-limit aware behavior.
- Continue streaming tarball verification and add larger-file benchmarks.
- Avoid redownloading existing valid tarballs even when state is absent.
- Add configurable output naming strategies.
- Add summary output with total packages, bytes, downloaded, skipped, failed, and elapsed time.
- Add machine-readable JSON report output.
- Add partial failure handling that records failed packages in state.
- Add resume mode that retries only missing/failed packages.

## State File Work

- Version the state file schema.
- Track package name, version, resolved URL, integrity, shasum, output path, size, and timestamps.
- Track packument metadata cache keys separately from downloaded tarballs.
- Track failures with retry counts and last error.
- Validate state against existing files at startup.
- Add `state inspect` or similar command if useful.

## Test Framework Work

- Keep fast unit tests independent from the public registry.
- Keep mock registry tests for deterministic resolver and fetch behavior.
- Expand npm parity tests with generated fixtures.
- Compare tarball URL sets against npm-generated `package-lock.json`.
- Compare resolved package name/version sets against npm.
- Add fixtures for peer dependencies.
- Add fixtures for peer dependency conflicts and strict/legacy peer modes.
- Add fixtures for optional dependencies.
- Add fixtures for overrides.
- Add fixtures for aliases.
- Add fixtures for scoped packages.
- Add fixtures for dist-tags and prereleases.
- Add fixtures for conflicting ranges.
- Add fixtures for lockfile v1, v2, and v3.
- Add fixtures for workspaces.
- Add fixtures for platform-filtered packages.
- Add large-package-tree performance benchmark.
- Add benchmark comparing this downloader against npm cache/package acquisition where practical.

## CLI Work

- Preserve `fetch` and `resolve`.
- Continue hardening `--registry`.
- Expand scoped registry support from `.npmrc`.
- Expand auth token support from `.npmrc` and environment.
- Add `--include-dev` and `--include-optional` behavior matching npm.
- Add `--omit` / `--include` flags matching npm naming.
- Add `--json` output.
- Add `--dry-run`.
- Add `--fail-fast`.
- Continue hardening `--max-retries`.
- Continue hardening `--metadata-cache`.
- Continue hardening `--metadata-cache-ttl`.
- Continue hardening `--metadata-retries`.
- Continue hardening `--offline` for cached metadata/state workflows.

## Documentation Work

- Document supported inputs.
- Document current parity limitations.
- Document state file format.
- Document air-gap workflow.
- Document how to run parity tests.
- Document how `cli-11.14.0` is used as local reference source.
- Document unsupported npm features until they are implemented.

## Verification Commands

```sh
go test ./...
go build ./cmd/golden-retriever
go run ./cmd/golden-retriever resolve --input cli-11.14.0/package-lock.json
NPM_PARITY=1 go test ./...
```
