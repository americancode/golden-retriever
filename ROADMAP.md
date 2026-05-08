# Roadmap

## Goal

Build a Go CLI that acquires every npm package tarball needed for an air-gapped install and pushes missing package versions to a target npm-compatible registry. The tool must accept `package.json`, `package-lock.json`, and `npm-shrinkwrap.json`, resolve or import the entire dependency tree, download all required `.tgz` files into a target directory, and publish missing packages to the target registry.

The npm CLI 11.14.0 source in `cli-11.14.0` is the local behavioral reference. The Go implementation should reproduce npm's resolution behavior natively. The practical success condition is: run this tool against a known `package.json`, have it acquire and push missing tarballs to an authenticated target npm-compatible registry, configure npm to use that target registry, and have `npm install` complete correctly from that target.

## Current State

- Git repository initialized.
- Go module created as `golden-retriever`.
- CLI entrypoint exists at `cmd/golden-retriever`.
- `fetch` command resolves/imports a package set and downloads tarballs.
- `resolve` command prints the resolved package tarball set.
- Lockfile v1 nested dependency import and v2/v3 `packages` import work for `package-lock.json` and `npm-shrinkwrap.json`.
- Directory inputs prioritize `npm-shrinkwrap.json` over `package-lock.json`, then fall back to `package.json`.
- Lockfile imports derive default registry tarball URLs for registry package entries that omit `resolved`, while preserving integrity metadata.
- `package.json` inputs now fail explicitly on invalid package names, invalid registry tag names, and unsupported dependency specs such as `file:`, `link:`, git, hosted git, directory, tarball URL, and `workspace:` specs instead of silently omitting them.
- `package.json` registry dependency walking works for basic registry semver specs.
- npm alias specs like `npm:pkg@range` are supported for tarball acquisition.
- Range support now covers more npm-style cases, including partial versions, caret and tilde partial ranges, hyphen ranges, prerelease ordering, wildcard range handling, and deprecated-version avoidance.
- Registry packument requests are coalesced so concurrent requests for the same package share one in-flight fetch.
- Dependency resolution now resolves independent child dependencies concurrently.
- Resolved graphs now retain root placement, dependency edges, edge types, and peer dependency edges in addition to the flat tarball set.
- Non-optional peer dependencies can be auto-placed at the parent location when no ancestor satisfies them.
- Optional peer dependencies are recorded without auto-installing when unsatisfied.
- Optional peer dependency conflicts are not treated as problem conflicts in normal mode, while `--strict-peer-deps` still fails them.
- Optional peer dependencies prefer an existing satisfying graph node over an incompatible ancestor when one is available.
- Optional peer edges that were initially missing are reconciled to an already-present satisfying node after dependency resolution completes.
- Auto-placed peer dependencies can be reconciled to a version satisfying multiple overlapping peer ranges at the same placement.
- Peer conflicts are detected when an ancestor/root candidate exists but does not satisfy the requested peer range.
- `--legacy-peer-deps` ignores peer dependencies.
- `--strict-peer-deps` fails on optional peer conflicts in addition to required peer conflicts.
- Resolver skips `bundleDependencies` / `bundledDependencies` child tarballs because npm expects those contents to be provided by the parent tarball.
- Resolver treats duplicate `optionalDependencies` entries as overriding `dependencies`.
- Resolver applies npm-style `os`, `cpu`, and explicit `libc` platform filters, including `"any"` platform rules, skipping incompatible optional packages and failing incompatible required packages.
- Resolver applies `engines.node` checks when `--node-version` is provided: manifest selection prefers engine-compatible range matches, strict mode fails incompatible required packages, while incompatible optional packages are skipped.
- Omitted dev, optional, and peer dependencies are not resolved and therefore skip engine/platform install checks for those omitted dependency types.
- Resolver ignores failed optional dependency resolution and rolls back failed optional subtrees so missing optional packages do not enter the tarball set.
- Semver prerelease range handling now follows npm's same-version-tuple rule for prerelease candidates.
- Root `package.json#overrides` supports top-level package overrides, nested ancestry overrides, object `"."` self overrides, version-qualified parent selectors, more-specific child selectors, nested and top-level `$` references to root dependency specs, and direct-dependency conflict checks for registry dependencies, devDependencies, optionalDependencies, and peerDependencies.
- Override empty strings and empty override objects are coerced to wildcard `*`, matching npm Arborist override behavior.
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
- A JSON state file tracks target registry inventory separately from local downloaded tarballs.
- The state file is the primary inventory source and is intended to be reused from CI cache, such as GitLab cache.
- `fetch` skips packages marked as present in the target registry.
- `state sync-target` resolves an input package set, queries a target registry, and marks package versions already present in `state.target`.
- `state mark-target` can mark a package/version as already present in the target registry.
- Target registry sync should run in parallel where possible.
- `push` publishes locally fetched tarballs missing from the target registry, runs in parallel, authenticates through `.npmrc`/CI credentials, and updates `state.target` after successful publication.
- Tarball integrity verification streams data and supports `sha512` SRI, `sha1` SRI, and legacy `sha1` shasum.
- Tests cover semver basics, lockfile import, mock registry resolution/fetching, state reuse, and opt-in npm parity.

## Required Outcomes From Initial Prompt

- Accept `package.json` as input.
- Accept npm lockfiles as input.
- Pull npm package tarballs into a target directory.
- Push missing package versions to a target npm-compatible registry.
- Build the whole dependency tree, including sub-dependencies, not only root dependencies.
- Ensure every package pulled matches what npm would pull.
- Build a test framework using npm for parity checks.
- Make package acquisition very efficient.
- Download as much as possible in parallel.
- Maintain a persistent local state file tracking what package versions are already present in the target registry, so they are not fetched or pushed again.
- Use local state as the normal source of truth; optionally query the target registry to rebuild or verify state.
- Support GitLab cache use for the local state and metadata cache.
- Run as a GitLab CI job with non-interactive registry authentication from CI variables.
- Reference npm CLI source code as the guide.
- Implement the resolver in Go rather than shelling out to npm.
- Optimize for acquiring all `.tgz` files, not for creating `node_modules`.

## Resolution Parity Work

- Continue porting npm package spec parsing behavior from `npm-package-arg` beyond current registry, alias, invalid-name, invalid-tag, and unsupported-spec classification.
- Port npm manifest selection behavior from `npm-pick-manifest`.
- Replace the current minimal semver implementation with npm-compatible semver behavior.
- Expand alias handling beyond registry aliases if npm requires it.
- Continue hardening dist-tags, exact versions, ranges, prereleases, hyphen ranges, and OR ranges against npm parity fixtures.
- Continue hardening `overrides`, including full selector semantics and npm parity fixtures.
- Continue hardening `peerDependencies`.
- Continue hardening `peerDependenciesMeta.optional`.
- Continue reproducing npm Arborist peer conflict behavior, advanced peer set grouping, and strict/legacy peer mode edge cases.
- Finish optional dependency shared-subtree semantics from Arborist `optional-set.js`.
- Expand bundled dependency parity for complete metadata, legacy bundling fixtures, and root bundler cases.
- Finish npm `--omit` / `--include` parity edge cases beyond current dev/optional/peer engine and platform check coverage.
- Support `workspaces`.
- Support `workspace:` specs if needed for package tree inputs, or keep explicit unsupported errors with parity tests.
- Support `file:`, `link:`, tarball URL, Git, GitHub, and hosted Git specs where required, or continue hardening explicit unsupported errors with parity tests.
- Continue hardening platform filter parity with npm fixtures, including automatic libc detection if needed.
- Finish `engines` handling for warning/report behavior and omit/include interactions.
- Support deprecation metadata handling where npm uses it for selection or warnings.
- Finish ancient lockfile edge cases and incomplete lock metadata behavior.
- Finish npm shrinkwrap behavior for bundled/shrinkwrapped package edge cases.
- Model remaining npm config that affects resolution, especially prefer-dedupe and install strategy.

## Acquisition Performance Work

- Continue improving concurrent packument fetching during dependency resolution.
- Add cache pruning and explicit invalidation commands.
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

- Continue versioning the state file schema.
- Document and test GitLab CI cache layout for `.gr/state.json`, `.gr/metadata`, and downloaded tarballs when useful.
- Track package name, version, resolved URL, integrity, shasum, output path, size, and timestamps.
- Track packument metadata cache keys separately from downloaded tarballs.
- Continue hardening target registry query/sync, including registry-specific auth and retry behavior.
- Keep target registry query/sync optional for rebuilding or verifying state.
- Continue hardening authenticated push/publish workflow against real target registries.
- Track failures with retry counts and last error.
- Validate state against existing files at startup.
- Add `state inspect` or similar command if useful.

## Test Framework Work

- Expand npm parity tests with generated fixtures.
- Compare tarball URL sets against npm-generated `package-lock.json`.
- Compare resolved package name/version sets against npm.
- Expand fixtures for peer dependency placement, conflicts, strict/legacy modes, and peer optional re-resolution.
- Expand fixtures for optional dependency shared-subtree behavior and tarball failure behavior.
- Expand fixtures for bundled dependency edge cases.
- Expand fixtures for overrides.
- Expand fixtures for aliases and scoped packages.
- Expand fixtures for dist-tags and prereleases.
- Add fixtures for conflicting ranges.
- Add fixtures for lockfile v1, v2, and v3.
- Add fixtures for workspaces.
- Expand fixtures for platform-filtered packages, including CPU combinations and real npm parity cases.
- Add large-package-tree performance benchmark.
- Add benchmark comparing this downloader against npm cache/package acquisition where practical.

## Missing Tests From Local npm CLI 11.14.0 Audit

These should be implemented as Go unit tests or npm-backed parity fixtures where they affect package resolution, tarball acquisition, target publish behavior, or CI auth. The local reference files are under `cli-11.14.0`.

- Arborist ideal tree parity from `workspaces/arborist/test/arborist/build-ideal-tree.js`:
  - Engine checks: expand coverage for warnings when engine strict is false beyond current strict omit fixtures.
  - Platform checks: expand root/transitive coverage beyond current omit fixtures and add automatic libc detection if needed.
  - Peer dependency placement: overlap cases, nested peers, unresolvable peers, cyclic peers, peer set conflicts/warnings, and legacy shrinkwrap peer cases.
  - Peer optional re-resolution: expand issue #8726 coverage beyond current missing-then-satisfied reconciliation, including cases where optional peer constraints force a previously chosen dependency version to be re-resolved and lockfile cases.
  - Peer optional existing-node preference: expand issue #9249 coverage beyond the current existing-satisfying-node fixture, including hoisting behavior where it changes placement.
  - Dedupe and placement modes: default placement, `preferDedupe`, `legacyBundling`, duplicated transitive deps, and explicit request placement behavior where they change selected package versions.
  - Bundle dependency cases: complete bundle metadata, bundled metadata dependency duplication, root bundler, two bundled deps, and legacy bundling bundle fixtures.
  - Shrinkwrapped dependency behavior: do not add/update shrinkwrapped deps by default, behavior with `complete:true`, bad shrinkwrap handling, and legacy shrinkwrap resolution.
  - Yarn lock influence: use `yarn.lock` versions/resolutions where npm would when package lock data is absent or incomplete.
  - Workspace resolution: simple/non-simplistic workspaces, workspace root links, workspace overrides, conflicting workspace dev deps, and workspace-specific peer set behavior.

- Arborist placement and peer internals from `workspaces/arborist/test/place-dep.js`, `can-place-dep.js`, and `peer-entry-sets.js`:
  - Can-place decision matrix for keeping, replacing, nesting, and conflicting dependency candidates.
  - Peer entry set grouping beyond current basic overlapping peer range reconciliation.
  - Placement tests where the same package name appears at multiple depths with incompatible ranges.

- Overrides from `workspaces/arborist/test/override-set.js` and `workspaces/arborist/test/arborist/build-ideal-tree.js`:
  - Parent spec mismatch should not apply an override.
  - Override semantic conflict detection when ranges intersect or do not intersect.
  - Alias, directory, file, and git override specs should be explicitly supported or explicitly rejected with tests.
  - Overrides inside cyclic dependency chains and overrides that fix peer ERESOLVE cases.

- Lockfile and shrinkwrap parity from `workspaces/arborist/test/shrinkwrap.js` and `spec-from-lock.js`:
  - Expand ancient lockfile shapes beyond current v1/v2/v3 import coverage.
  - Handle package entries missing `dependencies` or `integrity`.
  - Expand lock metadata derivation when entries only have one of `resolved` or `integrity`, especially non-default registries.
  - Ignore malformed lockfiles or fail deterministically according to npm-compatible input mode.
  - Resolve from lock metadata for aliased packages and scoped packages.
  - Decide and test whether hidden lockfiles under `node_modules/.package-lock.json` are intentionally unsupported.

- Package spec and dependency validation from `workspaces/arborist/test/dep-valid.js`, `node_modules/npm-package-arg/lib/npa.js`, and `node_modules/npm-pick-manifest/lib/index.js`:
  - Registry specs for scoped packages, aliases, exact versions, dist-tags, hyphen ranges, prerelease ranges, and conflicting ranges.
  - Expand invalid tag name and invalid request coverage.
  - Expand unsupported spec class coverage for file, link, git, hosted git, remote tarball URL, and directory specs beyond current deterministic root validation tests.
  - Expand deprecated version selection and latest-dist-tag preference fixtures beyond the current wildcard/latest/range coverage.
  - Dist-tag and prerelease publish/install interactions where npm avoids unsafe `latest` selection.

- Optional dependency and omit/include behavior from `workspaces/arborist/test/optional-set.js`, `build-ideal-tree.js`, and install command tests:
  - Expand optional failure coverage for tarball download failures and shared optional subtrees.
  - Optional metadependency failures are ignored only when the optional ancestor is the reason for inclusion; add shared-subtree cases from `optional-set.js`.
  - Expand matching `--include` coverage for engine/platform checks and lockfile parity.

- Registry/auth/config behavior from `workspaces/config/test/nerf-dart.js`, `env-replace.js`, `parse-field.js`, and npm publish command auth tests:
  - Full nerf-dart matching for registry auth keys, including path-specific registry auth and scoped registries.
  - Environment replacement edge cases in `.npmrc`.
  - Token, `_auth`, username/password, bare `_auth`, scoped `_auth`, and auth precedence tests for source and target registries.
  - Auth missing behavior for default, configured, and scoped registries.

- Publish/push parity from `workspaces/libnpmpublish/test/publish.js` and `test/lib/commands/publish.js`:
  - Scoped publish endpoint/body shape.
  - Restricted access for scoped packages and refusal of `restricted` access for unscoped packages.
  - Refuse private packages and bad semver manifests.
  - Conflict/existing version handling.
  - `publishConfig.registry` behavior should be explicitly supported or intentionally ignored in favor of `--target-registry`.
  - Prerelease dist-tag safety behavior should be considered for push, even though this tool mainly mirrors existing tarballs.
  - GitLab OIDC/provenance tests are not required for initial target push, but should remain a documented non-goal unless added later.

## CLI Work

- Continue hardening `push` command for authenticated parallel target registry publication.
- Continue hardening `--registry`.
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
- Document GitLab CI usage, cache keys, expected variables, and `.npmrc` auth examples.
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
