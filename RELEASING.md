# Release Process

## 1. Steps

1. Update the VERSION file with the new version (e.g., `v0.1.0`)
   ```bash
   printf "v0.1.0" > VERSION
   ```

2. Commit the VERSION change
   ```bash
   git add VERSION
   git commit -m "chore: bump version to v0.1.0"
   ```

3. Push to main branch
   ```bash
   git push origin main
   ```

The CI workflow will automatically:

- Create and push the tag from VERSION file
- Build binaries via GoReleaser
- Create GitHub Release with assets

## 2. Verify Release

1. Wait for [release workflow](https://github.com/i9wa4/tmux-a2a-postman/actions/workflows/release.yml) completion
2. Check [Releases page](https://github.com/i9wa4/tmux-a2a-postman/releases)
3. Verify release assets are attached

## 3. Notes

- VERSION file format: `v{major}.{minor}.{patch}` (e.g., `v0.1.0`)
- No trailing newline in VERSION file
- Release workflow triggers only when VERSION file changes
- GoReleaser generates binaries for multiple platforms automatically
- Version is embedded in binaries via ldflags during build
