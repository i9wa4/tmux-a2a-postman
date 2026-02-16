# Release Process

## Version Behavior

This project uses git tags as the single source of truth for versions.

**Local development builds:**
- Clean working tree: `git-abc1234` (7-character commit hash)
- Dirty working tree: `dev`
- Why: Nix flakes don't expose tag information to local builds (technical limitation)

**Tagged release builds (GitHub):**
- Building with `?ref=v0.2.0`: Shows `v0.2.0` (semantic version)
- Why: Nix sets `self.ref` attribute for remote references

**To verify version before release:**
```bash
# Local build (will show commit hash)
nix build
./result/bin/tmux-a2a-postman --version

# Simulated GitHub build (will show semantic version)
nix build github:i9wa4/tmux-a2a-postman?ref=v0.2.0
./result/bin/tmux-a2a-postman --version
```

## Release Steps

1. Commit all changes to main branch
2. Create and push annotated tag:
   ```bash
   git tag -a v0.2.0 -m "Release v0.2.0"
   git push origin v0.2.0
   ```
3. GitHub Actions automatically creates release with goreleaser

**Version verification:**
```bash
# Test GitHub build shows semantic version
nix build github:i9wa4/tmux-a2a-postman?ref=v0.2.0
./result/bin/tmux-a2a-postman --version
# Expected: v0.2.0
```

**Note:** Local builds show `git-abc1234` (commit hash). Only GitHub builds show `v0.2.0` (semantic version).

## Manual Release Trigger (Fallback)

If automatic tag trigger fails, manually trigger the workflow:

1. Go to [Actions tab](https://github.com/i9wa4/tmux-a2a-postman/actions/workflows/release.yml)
2. Click "Run workflow" button
3. Select branch/tag to release from
4. Click "Run workflow"

## Verify Release

1. Wait for [release workflow](https://github.com/i9wa4/tmux-a2a-postman/actions/workflows/release.yml) completion
2. Check [Releases page](https://github.com/i9wa4/tmux-a2a-postman/releases)
3. Verify release assets are attached

## Notes

- Release workflow triggers when git tag matching `v[0-9]*` is pushed
- Tag must be annotated (use `-a` flag)
- GoReleaser extracts version from git tag automatically
- Version is embedded in binaries via ldflags during build
- Manual trigger available as fallback (workflow_dispatch)

## Testing (Before Real Release)

To test the workflow without creating a real release:

1. Create test tag: `git tag -a v0.0.0-test -m "Test release"`
2. Push test tag: `git push origin v0.0.0-test`
3. Monitor [workflow runs](https://github.com/i9wa4/tmux-a2a-postman/actions)
4. Verify goreleaser completes successfully
5. Delete test tag:
   ```bash
   git tag -d v0.0.0-test
   git push --delete origin v0.0.0-test
   ```
6. Delete test release from GitHub Releases page
