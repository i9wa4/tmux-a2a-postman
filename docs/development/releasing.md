# Release Process

## 1. Release Steps

Release and skill-publish commands require an explicit release request from the
user at the time of execution. Do not infer release intent from PR work, skill
validation, Waza readiness, or review follow-up. In those cases, stop at normal
branch publication plus validation commands such as `nix run '.#skill-check'`
or `gh skill publish dist/skills --dry-run`.

### 1.1. Pre-merge review and enforcement

Before merging a release candidate PR, its orchestrator sends the exact head and
base SHAs, acceptance criteria, checks, and open findings through the verified
parent route for upper-session Guardian and independent Critic review. Hold the
merge if either review is missing or has an unresolved material finding. Their
reports are evidence for the human decision; they are not GitHub approving
reviews. The candidate also needs an eligible independent GitHub approval and
the required `ci` check on the exact PR head before merge. Recheck both after a
new push. A passing check or a review of an earlier head does not carry forward.

This repository's current `main` ruleset does **not** yet enforce that approval:
its required approving review count is zero, code-owner review has no
`CODEOWNERS` coverage, and a repository-role actor has an always-bypass entry.
An authorized administrator must first identify an eligible independent human
reviewer, require at least one approving review, dismiss stale approvals on new
pushes, require approval of the most recent push and resolution of review
threads, then remove or narrowly constrain the always-bypass role. If code-owner
review is retained, add valid `CODEOWNERS` coverage and an eligible owner. Read
back the effective ruleset and test both an unapproved PR that cannot merge and
an approved PR that can, without using a live release. The documentation PR
does not change those settings or establish enforcement by itself.

**Hold release publication until the remaining gates in
[#1112](https://github.com/i9wa4/tmux-a2a-postman/issues/1112) are installed and
verified.** A future workflow must prove the tag target is the exact merged PR
commit on `main` and that required `ci` succeeded for that same SHA. The current
tag workflow lacks this check, and a new workflow cannot guard older tagged
commits that still contain the old publisher. Tag creation or publishing
credentials need an effective separately approved control for those historical
refs. Do not infer safety from branch currency, an earlier PR-head check, or a
documentation change.

1. Merge the reviewed PR into main after the safeguards above are effective.
2. Run local pre-release checks:

   ```bash
   nix flake check
   nix build
   nix run '.#skill-check'
   nix develop .#cd --command goreleaser check
   ```

3. From the reviewed release commit, create and push only its annotated tag
   (must match `v[0-9]*`). Confirm `HEAD` and the freshly fetched
   `FETCH_HEAD` both match that commit before tagging:

   ```bash
   git fetch origin main &&
     test "$(git rev-parse HEAD)" = "$(git rev-parse FETCH_HEAD)" &&
     git tag -a vX.Y.Z -m vX.Y.Z &&
     git push --no-follow-tags origin refs/tags/vX.Y.Z:refs/tags/vX.Y.Z
   ```

4. GitHub Actions validates the skills with `nix run '.#skill-check'`
5. GoReleaser creates the GitHub Release and uploads binary archives and
   checksums

Do not run `gh skill publish --tag` from the tag-push workflow. That command
owns tag and release creation, so it fails when the pushed release tag already
exists. The tag-push release path validates the six generated
`dist/skills/*/SKILL.md` entries with `gh skill publish dist/skills --dry-run`;
the published Git tag and GitHub Release are
enough for `gh skill install` to resolve versions.

## 2. Version Behavior

- Tag must match `v[0-9]*` pattern (e.g., v0.2.0, v1.0.0)
- Local builds: show `git-abc1234` (commit hash) - Nix limitation
- GitHub Nix builds: show `vX.Y.Z` (semantic version) via `self.ref`
- GitHub GoReleaser builds: show `vX.Y.Z` via `{{.Tag}}` ldflag

## 3. Tag Ruleset

Release tags are expected to be immutable after publication. Configure a GitHub
tag ruleset for `v*` with:

- `Restrict updates`: enabled
- `Restrict deletions`: enabled
- `Restrict creations`: disabled unless tag creators are explicitly listed as
  bypass actors

## 4. Manual Release Trigger

The release workflow can be run manually from the
[Actions tab](https://github.com/i9wa4/tmux-a2a-postman/actions/workflows/release.yml).
The current manual run validates skills and invokes GoReleaser even when no
tag ref is present; it does not publish skills. This path is **not** exempt from
the release hold above and must not be used for publication while the gate is
missing. The planned correction keeps branch dispatch for validation only and
permits GoReleaser publication only for a tag that passes the same exact-commit,
merged-PR, and `ci` checks as a tag push. Document and verify that change before
using manual dispatch for a release.

## 5. Verify Release

Check [Releases page](https://github.com/i9wa4/tmux-a2a-postman/releases) for
completion. A successful release has:

- skills validated by `nix run '.#skill-check'`
- GoReleaser archives for darwin/linux amd64/arm64
- `checksums.txt`

After release, confirm install discovery works:

```bash
gh skill preview i9wa4/tmux-a2a-postman postman-send-message@vX.Y.Z
```
