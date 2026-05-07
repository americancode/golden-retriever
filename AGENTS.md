# Agents Guide

## Project Intent

This repository is building `golden-retriever`, a Go CLI for acquiring npm tarballs for air-gapped environments and publishing missing package versions to a target npm-compatible registry. The goal is to reproduce npm's dependency resolution behavior in Go and make the acquisition and push path fast, concurrent, target-registry-aware, authenticated, and integrity-checked.

The success workflow is: run this tool against a known `package.json`, acquire every tarball npm would need, push missing package versions into an authenticated target npm-compatible registry, configure npm to use that target registry, and have `npm install` complete correctly.

Do not turn this into a wrapper around `npm install`. npm may be used in tests as the oracle for parity, but production resolution and fetching should be native Go.

## Local Reference Source

The npm CLI 11.14.0 source lives in `cli-11.14.0`.

Use it as the reference for behavior, especially:

- `cli-11.14.0/workspaces/arborist`
- `cli-11.14.0/node_modules/pacote`
- `cli-11.14.0/node_modules/npm-package-arg`
- `cli-11.14.0/node_modules/npm-pick-manifest`
- `cli-11.14.0/node_modules/semver`
- `cli-11.14.0/node_modules/npm-registry-fetch`

`cli-11.14.0` is intentionally ignored by Git. Do not vendor or commit it unless the user explicitly changes that direction.

## Current Architecture

- `cmd/golden-retriever`: CLI entrypoint.
- `internal/npm/client.go`: npm registry packument client, request coalescing, disk metadata cache, ETag revalidation, and packument retry behavior.
- `internal/npm/config.go`: `.npmrc` registry and auth parsing.
- `internal/npm/input.go`: input dispatch for package and lock files.
- `internal/npm/lockfile.go`: lockfile import.
- `internal/npm/resolver.go`: package.json dependency resolver.
- `internal/npm/overrides.go`: root `package.json#overrides` parser and matcher for registry dependency overrides.
- `internal/npm/types.go`: resolved graph model, including package nodes, dependency edges, and peer edges.
- `internal/npm/semver.go`: current minimal semver/range support.
- `internal/npm/fetch.go`: concurrent tarball downloader, integrity checks, state file.
- `internal/npm/target.go`: target registry inventory sync.
- `internal/npm/*_test.go`: unit, mock-registry, and opt-in npm parity tests.

## Current Limitations

The implementation is an initial native slice. It does not yet have full npm Arborist parity.

Known gaps include:

- Full npm semver behavior beyond the currently implemented common range forms.
- Full npm package spec parsing.
- Peer dependency placement and conflict behavior.
- Full peer set grouping and remaining strict/legacy peer mode edge cases.
- Overrides.
- Override edge cases beyond top-level, nested ancestry, `"."` self overrides, `$` references, and direct-dependency conflict checks.
- Alias edge cases beyond registry aliases like `npm:pkg@range`.
- Workspaces.
- Platform/engine filtering.
- Bundled dependencies.
- Git, file, link, remote tarball, and hosted Git specs.
- Full lockfile v1 import.
- Full `.npmrc` behavior beyond default registry, scoped registry, and common auth keys.

See `ROADMAP.md` before choosing the next task.

## Development Rules

- Prefer small, testable parity increments.
- Add or update tests for each npm behavior ported.
- Use npm parity tests as an oracle, not as production implementation.
- Keep fast tests independent of the public registry.
- Keep public-registry/npm tests behind `NPM_PARITY=1`.
- Preserve target inventory state behavior when changing fetch or push logic. State exists primarily so the program knows what package versions the target registry already contains and can skip fetching and pushing them.
- Treat local state as the normal inventory source. Target registry querying is optional and should be used to rebuild or verify state.
- Design state and metadata cache paths so they work well with GitLab cache. This program is expected to run from GitLab CI.
- Target registry auth must be non-interactive and compatible with CI variables.
- Target registry sync and push should run in parallel where practical.
- Target registry push must support authentication.
- Preserve tarball integrity verification.
- Preserve concurrency, but avoid data races in shared state.
- Do not commit downloaded tarballs, generated binaries, state files, or the npm source reference tree.

## Useful Commands

```sh
go test ./...
go build ./cmd/golden-retriever
go run ./cmd/golden-retriever resolve --input cli-11.14.0/package-lock.json
NPM_PARITY=1 go test ./...
```

## Implementation Priorities

1. Continue hardening semver/spec parsing against npm parity fixtures.
2. Finish porting manifest picking from `npm-pick-manifest`.
3. Harden overrides against npm parity fixtures, especially full selector semantics.
4. Implement Arborist-compatible peer set grouping and remaining strict/legacy peer mode edge cases.
5. Add metadata cache pruning and explicit invalidation commands.
6. Add authenticated parallel target registry push/publish workflow that marks inventory after successful upload.
7. Expand `.npmrc` config compatibility.

## User Direction To Preserve

The user explicitly wants the Go program to reproduce npm logic, not avoid it because it is hard. Treat npm's source as available reference material and implement the behavior in Go.

The performance goal is acquiring all required `.tgz` files into a target directory and pushing missing versions to the target registry as fast as practical, with a persistent target inventory state file and parallel network work wherever resolution and registry constraints allow it.
