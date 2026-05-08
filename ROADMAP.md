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
- Lockfile v2/v3 `packages` import works for `package-lock.json` and `npm-shrinkwrap.json`.
- `package.json` registry dependency walking works for basic registry semver specs.
- npm alias specs like `npm:pkg@range` are supported for tarball acquisition.
- Range support now covers more npm-style cases, including partial versions, hyphen ranges, prerelease ordering, and deprecated-version avoidance.
- Registry packument requests are coalesced so concurrent requests for the same package share one in-flight fetch.
- Dependency resolution now resolves independent child dependencies concurrently.
- Resolved graphs now retain root placement, dependency edges, edge types, and peer dependency edges in addition to the flat tarball set.
- Non-optional peer dependencies can be auto-placed at the parent location when no ancestor satisfies them.
- Optional peer dependencies are recorded without auto-installing when unsatisfied.
- Peer conflicts are detected when an ancestor/root candidate exists but does not satisfy the requested peer range.
- `--legacy-peer-deps` ignores peer dependencies.
- `--strict-peer-deps` fails on optional peer conflicts in addition to required peer conflicts.
- Resolver skips `bundleDependencies` / `bundledDependencies` child tarballs because npm expects those contents to be provided by the parent tarball.
- Resolver treats duplicate `optionalDependencies` entries as overriding `dependencies`.
- Resolver applies npm-style `os` and `cpu` platform filters, skipping incompatible optional packages and failing incompatible required packages.
- Resolver applies `engines.node` checks when `--node-version` is provided: strict mode fails incompatible required packages, while incompatible optional packages are skipped.
- Resolver ignores failed optional dependency resolution and rolls back failed optional subtrees so missing optional packages do not enter the tarball set.
- Semver prerelease range handling now follows npm's same-version-tuple rule for prerelease candidates.
- Root `package.json#overrides` supports top-level package overrides, nested ancestry overrides, object `"."` self overrides, `$` references to root dependency specs, and direct-dependency conflict checks for registry dependencies.
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

- Port npm package spec parsing behavior from `npm-package-arg`.
- Port npm manifest selection behavior from `npm-pick-manifest`.
- Replace the current minimal semver implementation with npm-compatible semver behavior.
- Expand alias handling beyond registry aliases if npm requires it.
- Continue hardening dist-tags, exact versions, ranges, prereleases, wildcards, hyphen ranges, and OR ranges against npm parity fixtures.
- Continue hardening `overrides`, including full selector semantics and npm parity fixtures.
- Continue hardening `peerDependencies`.
- Continue hardening `peerDependenciesMeta.optional`.
- Continue reproducing npm Arborist peer conflict behavior, peer set grouping, and strict/legacy peer mode edge cases.
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

- Continue versioning the state file schema.
- Treat target registry inventory as the primary state model.
- Treat locally maintained state as the normal source of truth.
- Support CI cache workflows, especially GitLab cache, for state and metadata cache reuse.
- Document and test GitLab CI cache layout for `.gr/state.json`, `.gr/metadata`, and downloaded tarballs when useful.
- Track package name, version, resolved URL, integrity, shasum, output path, size, and timestamps.
- Track packument metadata cache keys separately from downloaded tarballs.
- Continue hardening target registry query/sync, including registry-specific auth and retry behavior.
- Keep target registry query/sync optional for rebuilding or verifying state.
- Continue hardening authenticated push/publish workflow against real target registries.
- Run target state rebuild/sync in parallel where possible.
- Target registry operations support non-interactive GitLab CI credentials from `.npmrc` expansion or direct environment variables.
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
- Add fixtures for bundled dependencies.
- Add fixtures for overrides.
- Add fixtures for aliases.
- Add fixtures for scoped packages.
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
  - Engine checks: expand coverage for warnings when engine strict is false and respect omit flags for dev/optional/peer dependency engine checks.
  - Platform checks: fail required incompatible packages and ignore optional incompatible packages, with both root and transitive cases.
  - Peer dependency placement: overlap cases, nested peers, unresolvable peers, cyclic peers, peer set conflicts/warnings, and legacy shrinkwrap peer cases.
  - Peer optional re-resolution: issue #8726 cases where optional peer constraints force a previously chosen dependency version to be re-resolved, including fresh install and lockfile cases.
  - Peer optional existing-node preference: issue #9249 behavior where existing tree nodes are preferred over registry fetches when satisfying peer optional edges.
  - Dedupe and placement modes: default placement, `preferDedupe`, `legacyBundling`, duplicated transitive deps, and explicit request placement behavior where they change selected package versions.
  - Bundle dependency cases: empty bundle metadata, complete bundle metadata, bundled metadata dependency duplication, root bundler, two bundled deps, and legacy bundling bundle fixtures.
  - Shrinkwrapped dependency behavior: do not add/update shrinkwrapped deps by default, behavior with `complete:true`, bad shrinkwrap handling, and legacy shrinkwrap resolution.
  - Yarn lock influence: use `yarn.lock` versions/resolutions where npm would when package lock data is absent or incomplete.
  - Workspace resolution: simple/non-simplistic workspaces, workspace root links, workspace overrides, conflicting workspace dev deps, and workspace-specific peer set behavior.

- Arborist placement and peer internals from `workspaces/arborist/test/place-dep.js`, `can-place-dep.js`, and `peer-entry-sets.js`:
  - Can-place decision matrix for keeping, replacing, nesting, and conflicting dependency candidates.
  - Peer entry set grouping for overlapping peer sets and conflict detection.
  - Placement tests where the same package name appears at multiple depths with incompatible ranges.

- Overrides from `workspaces/arborist/test/override-set.js` and `workspaces/arborist/test/arborist/build-ideal-tree.js`:
  - Empty-string override coerces to `*`.
  - Version-qualified parent override selectors.
  - More-specific child override priority.
  - Parent spec mismatch should not apply an override.
  - Direct dependency, devDependency, and peerDependency override conflict errors.
  - Override references with `$dependency`, including top-level identifier omitted.
  - Override semantic conflict detection when ranges intersect or do not intersect.
  - Alias, directory, file, and git override specs should be explicitly supported or explicitly rejected with tests.
  - Overrides inside cyclic dependency chains and overrides that fix peer ERESOLVE cases.

- Lockfile and shrinkwrap parity from `workspaces/arborist/test/shrinkwrap.js` and `spec-from-lock.js`:
  - Prioritize `npm-shrinkwrap.json` over `package-lock.json`.
  - Import v1, v2, v3, and ancient lockfile shapes.
  - Handle package entries missing `dependencies`, `resolved`, or `integrity`.
  - Preserve/derive integrity when lock metadata only has one of `resolved` or `integrity`.
  - Ignore malformed lockfiles or fail deterministically according to npm-compatible input mode.
  - Resolve from lock metadata for aliased packages and scoped packages.
  - Decide and test whether hidden lockfiles under `node_modules/.package-lock.json` are intentionally unsupported.

- Package spec and dependency validation from `workspaces/arborist/test/dep-valid.js`, `node_modules/npm-package-arg/lib/npa.js`, and `node_modules/npm-pick-manifest/lib/index.js`:
  - Registry specs for scoped packages, aliases, exact versions, dist-tags, wildcard ranges, hyphen ranges, prerelease ranges, and conflicting ranges.
  - Invalid tag names and invalid requests.
  - Unsupported spec classes: file, link, git, hosted git, remote tarball URL, and directory specs should have tests documenting skip/error behavior for this air-gap registry use case.
  - Deprecated version selection and latest-dist-tag preference behavior.
  - Dist-tag and prerelease publish/install interactions where npm avoids unsafe `latest` selection.

- Optional dependency and omit/include behavior from `workspaces/arborist/test/optional-set.js`, `build-ideal-tree.js`, and install command tests:
  - Expand optional failure coverage for tarball download failures and shared optional subtrees.
  - Optional metadependency failures are ignored only when the optional ancestor is the reason for inclusion; add shared-subtree cases from `optional-set.js`.
  - `--omit=dev`, `--omit=optional`, `--omit=peer` and matching `--include` behavior should match npm's dependency graph and engine/platform checks.

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

- Preserve `fetch` and `resolve`.
- Continue hardening `push` command for authenticated parallel target registry publication.
- `mirror` command added for the combined CI workflow: resolve once, optionally sync target state, fetch missing tarballs, push missing tarballs, and update state.
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
