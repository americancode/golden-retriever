# Agents Guide

## Project Intent

This repository is building `golden-retriever`, a Go CLI for acquiring npm tarballs for air-gapped environments. The goal is to reproduce npm's dependency resolution behavior in Go and make the tarball acquisition path fast, concurrent, resumable, and integrity-checked.

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
- `internal/npm/types.go`: resolved graph model, including package nodes, dependency edges, and peer edges.
- `internal/npm/semver.go`: current minimal semver/range support.
- `internal/npm/fetch.go`: concurrent tarball downloader, integrity checks, state file.
- `internal/npm/*_test.go`: unit, mock-registry, and opt-in npm parity tests.

## Current Limitations

The implementation is an initial native slice. It does not yet have full npm Arborist parity.

Known gaps include:

- Full npm semver behavior beyond the currently implemented common range forms.
- Full npm package spec parsing.
- Peer dependency placement and conflict behavior.
- Peer dependency conflict behavior, peer set grouping, and strict/legacy peer modes.
- Overrides.
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
- Preserve resumable state behavior when changing fetch logic.
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
3. Implement Arborist-compatible peer dependency conflict behavior and strict/legacy peer modes.
4. Expand parity fixtures and compare against npm-generated lockfiles.
5. Add metadata cache pruning and explicit invalidation commands.
6. Expand `.npmrc` config compatibility.
7. Improve state schema and reporting.

## User Direction To Preserve

The user explicitly wants the Go program to reproduce npm logic, not avoid it because it is hard. Treat npm's source as available reference material and implement the behavior in Go.

The performance goal is acquiring all required `.tgz` files into a target directory as fast as practical, with a persistent state file and parallel fetching wherever resolution constraints allow it.
