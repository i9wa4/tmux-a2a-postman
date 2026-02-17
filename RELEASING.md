# Release Process

## Release Steps

1. Commit all changes to main branch
2. Create and push annotated tag (must match `v[0-9]*`):
   ```bash
   git tag -a vX.Y.Z -m "Release vX.Y.Z"
   git push origin vX.Y.Z
   ```
3. GitHub Actions automatically creates release with goreleaser

## Version Behavior

- Tag must match `v[0-9]*` pattern (e.g., v0.2.0, v1.0.0)
- Local builds: show `git-abc1234` (commit hash) - Nix limitation
- GitHub builds: show `vX.Y.Z` (semantic version) via `self.ref`

## Manual Release Trigger (Fallback)

If automatic tag trigger fails, use "Run workflow" on the
[Actions tab](https://github.com/i9wa4/tmux-a2a-postman/actions/workflows/release.yml).

## Verify Release

Check [Releases page](https://github.com/i9wa4/tmux-a2a-postman/releases) for
completion and attached assets.

## Testing (Pre-release Verification)

```bash
git describe --tags   # confirm tag is correct
nix build             # verify build succeeds
```
