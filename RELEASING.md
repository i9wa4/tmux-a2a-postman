# Release Process

## 1. Steps

1. Go to [GitHub Actions - release workflow](https://github.com/i9wa4/tmux-a2a-postman/actions/workflows/release.yml)
2. Click "Run workflow"
3. Enter the release tag name (e.g., `v0.1.0`)
4. Click "Run workflow" button

The workflow will automatically:

- Create the tag on main HEAD
- Push the tag to remote
- Build and publish release assets via GoReleaser

## 2. Verify Release

1. Wait for workflow completion
2. Check [Releases page](https://github.com/i9wa4/tmux-a2a-postman/releases)
3. Verify release assets are attached

## 3. Notes

- Tag format: `v{major}.{minor}.{patch}` (e.g., `v0.1.0`)
- No need to create tags manually
- GoReleaser generates binaries for multiple platforms automatically
