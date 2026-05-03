# Release Process

## 1. Release Steps

1. Commit all changes to main branch
2. Run local pre-release checks:

   ```bash
   nix flake check
   nix build
   nix run .#skill-check
   nix develop .#cd --command goreleaser check
   ```

3. Create and push an annotated tag (must match `v[0-9]*`):

   ```bash
   git tag -a vX.Y.Z -m vX.Y.Z
   git push origin main --tags
   ```

4. GitHub Actions validates the skills with `nix run .#skill-check`
5. GoReleaser creates the GitHub Release and uploads binary archives and
   checksums

Do not run `gh skill publish --tag` from the tag-push workflow. That command
owns tag and release creation, so it fails when the pushed release tag already
exists. The tag-push release path validates `skills/*/SKILL.md` with
`gh skill publish --dry-run`; the published Git tag and GitHub Release are
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
Manual runs validate skills and run GoReleaser, but they do not publish skills
because no tag ref is present.

## 5. Verify Release

Check [Releases page](https://github.com/i9wa4/tmux-a2a-postman/releases) for
completion. A successful release has:

- skills validated by `nix run .#skill-check`
- GoReleaser archives for darwin/linux amd64/arm64
- `checksums.txt`

After release, confirm install discovery works:

```bash
gh skill preview i9wa4/tmux-a2a-postman postman-send-message@vX.Y.Z
```
