# Roadmap

## Required Outcomes

- [ ] Accept `package.json` inputs with npm-compatible dependency resolution.
- [ ] Accept `package-lock.json` and `npm-shrinkwrap.json` inputs with npm-compatible tarball import.
- [ ] Resolve the full dependency tree, including all sub-dependencies needed for `npm install`.
- [ ] Fetch every required package tarball into a target directory.
- [ ] Maintain local state for target-registry inventory so already-present package versions are not fetched or pushed again.
- [ ] Optionally query the target registry to rebuild or verify local state.
- [ ] Push missing package versions to an authenticated target npm-compatible registry.
- [ ] Run efficiently in GitLab CI using cached state, cached metadata, and CI-provided credentials.
- [ ] Preserve native Go resolution and acquisition; do not shell out to npm in production code.
- [ ] Keep npm CLI 11.14.0 in `cli-11.14.0` as the local behavioral reference.
- [ ] Prove parity with npm using automated npm-backed tests where practical.

## Acquisition TODOs

- [ ] Continue improving concurrent packument fetching while preserving deterministic npm-compatible output.
- [ ] Continue hardening retry behavior for transient packument failures.
- [ ] Continue hardening retry behavior for transient tarball failures.
- [ ] Add larger-file streaming verification benchmarks.
- [ ] Add byte accounting to push reports if useful for registry throughput analysis.
- [ ] Add resume mode that retries only missing or failed packages.
- [ ] Benchmark this downloader against npm cache/package acquisition where practical.

## State TODOs

- [ ] Continue versioning the state file schema as migrations are needed.
- [ ] Add broader state migration tests as schema versions are introduced.
- [ ] Track packument metadata cache keys separately from downloaded tarballs.
- [ ] Continue hardening target registry query/sync for registry-specific auth behavior.
- [ ] Continue hardening target registry query/sync for registry-specific retry behavior.
- [ ] Keep target registry query/sync optional for rebuilding or verifying state.
- [ ] Continue hardening authenticated push/publish workflow against real target registries.
- [ ] Expand `state inspect` with package-level filtering if useful.

## Test Framework TODOs

- [ ] Expand npm parity tests with generated fixtures.
- [ ] Compare tarball URL sets against npm-generated `package-lock.json`.
- [ ] Compare resolved package name/version sets against npm.
- [ ] Add peer dependency placement parity fixtures.
- [ ] Add peer conflict parity fixtures.
- [ ] Add strict and legacy peer mode parity fixtures.
- [ ] Add peer optional re-resolution parity fixtures.
- [ ] Add optional dependency shared-subtree parity fixtures.
- [ ] Add bundled dependency parity fixtures.
- [ ] Add override parity fixtures.
- [ ] Add alias and scoped package parity fixtures.
- [ ] Add dist-tag and prerelease parity fixtures.
- [ ] Add conflicting range parity fixtures.
- [ ] Add lockfile v1, v2, and v3 parity fixtures.
- [ ] Add workspace parity fixtures.
- [ ] Add platform-filtered package parity fixtures.
- [ ] Add CPU combination parity fixtures.
- [ ] Add real npm package parity cases that have historically broken resolution.
- [ ] Add a regression parity fixture for Angular real-world delta (assert exact package set parity vs npm lockfile, including `conventional-commits-filter@5.0.0` and no extra toolchain subtree).

## Registry/Auth TODOs

- [ ] Add full nerf-dart matching for registry auth keys.
- [ ] Add path-specific registry auth fixtures.
- [ ] Add scoped registry auth fixtures for source and target registries.
- [ ] Add `.npmrc` environment replacement edge cases.
- [ ] Add `.npmrc` parse-field edge cases.
- [ ] Expand token, `_auth`, username/password, bare `_auth`, scoped `_auth`, and auth precedence tests.
- [ ] Add auth-missing behavior for default, configured, and scoped registries.
- [ ] Add end-to-end GitLab package registry publish/sync fixture when a real target registry is available.

## Publish/Push TODOs

- [ ] Expand scoped publish endpoint/body shape fixtures.
- [ ] Expand restricted access fixtures for scoped packages.
- [ ] Refuse `restricted` access for unscoped packages.
- [ ] Expand private package refusal fixtures.
- [ ] Expand bad semver manifest refusal fixtures.
- [ ] Expand conflict/existing version handling fixtures.
- [ ] Decide whether `publishConfig.registry` is supported or intentionally ignored in favor of `--target-registry`.
- [ ] Consider prerelease dist-tag safety behavior for push.
- [ ] Document GitLab OIDC/provenance as a non-goal unless added later.

## Proposals

- [ ] Add an installed-tree mode for `node_modules/.package-lock.json` only if mirroring an existing install becomes a product requirement.
- [ ] Add support for non-registry spec classes only when target package trees require them.
- [ ] Add package-level filtering to `state inspect` if state files become large enough to need it.
- [ ] Add push byte accounting if registry throughput analysis becomes useful.
- [ ] Add GitLab package registry end-to-end tests behind an opt-in environment gate.
