# Release Process

## 1. Steps

1. Update the VERSION file with the new version (e.g., `v0.2.0`)
   ```bash
   printf "v0.2.0" > VERSION
   ```

   ⚠️ **IMPORTANT:** VERSION file must match git tag exactly
   - VERSION file is used by Nix builds (flake.nix)
   - Git tag is used by release workflow and goreleaser
   - Mismatch will cause version inconsistency

2. Commit the VERSION change
   ```bash
   git add VERSION
   git commit -m "chore: bump version to v0.2.0"
   ```

3. Create annotated git tag
   ```bash
   git tag -a v0.2.0 -m "Release v0.2.0"
   ```

   **Tag requirements:**
   - Must be annotated (`-a` flag)
   - Must start with `v` followed by digit (e.g., v0.2.0, v1.0.0)
   - Message format: `"Release v{version}"`

4. Push commit and tag to origin
   ```bash
   git push origin main
   git push origin v0.2.0
   ```

The CI workflow will automatically:

- Detect tag push (trigger: `tags: v[0-9]*`)
- Build binaries via GoReleaser
- Create GitHub Release with auto-generated CHANGELOG
- Attach release assets

## 2. Manual Release Trigger (Fallback)

If automatic tag trigger fails, manually trigger the workflow:

1. Go to [Actions tab](https://github.com/i9wa4/tmux-a2a-postman/actions/workflows/release.yml)
2. Click "Run workflow" button
3. Select branch/tag to release from
4. Click "Run workflow"

## 3. Verify Release

1. Wait for [release workflow](https://github.com/i9wa4/tmux-a2a-postman/actions/workflows/release.yml) completion
2. Check [Releases page](https://github.com/i9wa4/tmux-a2a-postman/releases)
3. Verify release assets are attached

## 4. Notes

- VERSION file format: `v{major}.{minor}.{patch}` (e.g., `v0.2.0`)
- No trailing newline in VERSION file
- Release workflow triggers when git tag matching `v[0-9]*` is pushed
- Tag must be annotated (use `-a` flag)
- GoReleaser extracts version from git tag automatically
- Version is embedded in binaries via ldflags during build
- Manual trigger available as fallback (workflow_dispatch)

## 5. Testing (Before Real Release)

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
