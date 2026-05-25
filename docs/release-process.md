# Release process

A `fdh` release is **atomic across two artifacts**: the cross-compiled Go binary and the `@askenaz-dev/fdh` npm package wrapping it. A single `git tag vX.Y.Z` triggers one pipeline that produces both with the **same version**. There are no independent cycles — `npm view @askenaz-dev/fdh version` always equals `fdh --version`.

## Anatomy of a release

```
┌───────────────────────────────────────────────────────────────────────┐
│  git tag v0.7.2                                                        │
│  git push --tags                                                       │
└──────────────────────────┬────────────────────────────────────────────┘
                           │ triggers .github/workflows/release.yml
                           ▼
┌─────────────────────────────────────────────────────────────────────────┐
│  job: goreleaser                                                        │
│  ────────────────                                                       │
│  - cross-compile Go for 5 targets                                       │
│  - emit fdh_v0.7.2_<os>_<arch>.tar.gz + .sha256 for each                │
│  - emit .deb + .rpm for linux                                           │
│  - upload-artifact dist-v0.7.2 (used by downstream jobs)                │
└──────────────────────────┬──────────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────────────┐
│  job: publish                                                           │
│  ─────────────                                                          │
│  - download dist-v0.7.2                                                 │
│  - PUT each binary + .sha256 to ${PKG_BASE_URL}/fdh/v0.7.2/<file>       │
│  - regenerate ${PKG_BASE_URL}/fdh/manifest.json (latest pointer)        │
│  - This is what install.sh / install.ps1 consume                        │
└──────────────────────────┬──────────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────────────┐
│  job: publish-npm                                                       │
│  ─────────────────                                                      │
│  - install dev deps, build TS, run tests                                │
│  - npm version 0.7.2 --no-git-tag-version --allow-same-version          │
│  - configure ~/.npmrc with NPM_INTERNAL_REGISTRY + token                │
│  - npm publish --access restricted                                      │
│  - Consumers: npx @askenaz-dev/fdh init  /  npm i -g @askenaz-dev/fdh       │
└─────────────────────────────────────────────────────────────────────────┘
```

## Cutting a release (the happy path)

```sh
# 1. Ensure main is green
gh run list --branch main --limit 3

# 2. Decide the version (semver). Examples:
#    - bug fix only:  v0.7.3
#    - new spec / capability: v0.8.0
#    - breaking change: v1.0.0 (after deprecation window)

# 3. Tag and push
git tag v0.7.3
git push origin v0.7.3
```

The workflow runs automatically. Watch it:

```sh
gh run watch
```

When it's green, validate:

```sh
fdh --version                          # the binary you have locally — may be old
npm view @askenaz-dev/fdh version        # what the registry now offers
curl -fsSL https://api.github.com/repos/askenaz-dev/forge-development-hub-cli/releases/latest | jq '.tag_name'
```

All three should converge on `v0.7.3` after the publish job completes.

## Dry-run / pre-release

To validate the pipeline without publishing:

```sh
# Trigger via workflow_dispatch — the npm step skips publish on non-tag refs.
gh workflow run release.yml -f version=v0.0.0-rc.1
```

The `goreleaser` job builds artifacts. The `publish` job uploads them only if `PKG_BASE_URL` is set. The `publish-npm` job runs the build + tests + `npm publish --dry-run` (no actual publish on non-tag).

## Versioning rules

| Change type | Bump | Example |
|---|---|---|
| Bug fix, doc-only, internal refactor | patch | `0.7.2 → 0.7.3` |
| New capability / new CLI flag / new skill | minor | `0.7.3 → 0.8.0` |
| Breaking CLI flag, removed command, schema bump | major (post-1.0) | `1.0.0 → 2.0.0` |
| Pre-1.0 breaking change | minor | `0.7.0 → 0.8.0` (document loudly) |

## Things to know about the npm channel

- **First publish prerequisite:** the internal registry (JFrog Artifactory Pro / Sonatype Nexus 3 OSS / GitLab Package Registry — see `openspec/changes/archive/.../fdh-cli-npm-distribution/design.md` for the decision tree) must be provisioned with a virtual repo named `npm-internal` (or equivalent) and scope `@askenaz-dev/`. Repo vars `NPM_INTERNAL_REGISTRY` (URL) and secret `NPM_INTERNAL_TOKEN` (token with publish rights) must be set on the Actions secrets page.
- **Until those vars exist**, the `publish-npm` job runs `npm publish --dry-run` and prints the tarball contents — useful for verifying the file list (`dist/`, `README.md`) without actually publishing.
- **Versioning is 1:1 with the Go binary always.** Never bump the npm package independently. If the wrapper has a bug fix that doesn't change the Go binary, bump both (patch) anyway. This keeps `npm view ... version === fdh --version` always true.
- **`prepublishOnly` runs build + tests** before any publish — local `npm publish` from a dev machine is also safe.

## Rolling back

If a bad release ships:

```sh
# 1. Remove the bad version from npm (if published, within 72h):
npm unpublish @askenaz-dev/fdh@<bad-version>

# 2. Re-tag and re-release the previous good version:
git tag v0.7.4 v0.7.2^{commit}      # if 0.7.3 was bad, tag 0.7.4 from 0.7.2
git push origin v0.7.4

# 3. Manually revert the manifest.json pointer if needed:
PKG_BASE_URL=https://... gh workflow run release.yml -f version=v0.7.4
```

Consumers on the bad version: `npm rm -g @askenaz-dev/fdh && npm i -g @askenaz-dev/fdh@<good>`.

## Validation checklist for each release

- [ ] `gh run watch` is green for all jobs.
- [ ] `fdh --version` (after `npm rebuild`) reports the new version.
- [ ] `npm view @askenaz-dev/fdh version` matches.
- [ ] `curl ${PKG_BASE_URL}/fdh/manifest.json | jq .latest` matches.
- [ ] Smoke-test on at least one of each OS in the matrix (darwin-arm64, linux-amd64, windows-amd64).
- [ ] CHANGELOG entry added.
- [ ] Slack #fdh-users notified for non-patch releases.

## See also

- [`quickstart.md`](./quickstart.md)
- [`troubleshooting.md`](./troubleshooting.md)
- `openspec/specs/fdh-cli-distribution/spec.md` (in the hub repo)
- `openspec/specs/fdh-npm-wrapper/spec.md` (in the hub repo)
