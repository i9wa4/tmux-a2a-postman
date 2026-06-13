# Contributing

## 1. Local Checks

After implementation work:

- Stage new files before running Nix checks because flakes only see
  git-tracked files
- Run `nix flake check`
- Run `nix build`
- Check that `README.md` and `skills/*/SKILL.md` do not mention removed
  commands, renamed flags, or deleted packages

Tmux discovery integration checks are opt-in so the default test suite stays
usable without a live tmux server. To exercise the tmux-backed discovery lane:

```sh
TMUX_A2A_POSTMAN_TMUX_INTEGRATION=1 go test ./internal/discovery -run 'TestDiscoverNodes_With(ChildProcess|PaneTitle)' -count=1
```

Go version policy:

- Keep `go.mod` at major.minor (for example `go 1.26`)
- Keep `flake.nix` on the same major.minor (`pkgs.go_1_26`)
- Keep the `flake.nix` Go override on the latest required patch release only
  for the break-glass security window before `nixpkgs-unstable` catches up
- The scheduled `go-toolchain-vuln-detector` workflow is the actionable
  detector for standard-library and toolchain module findings; it fails with
  the finding list when a break-glass update may be needed
- Use the manual `update-go-toolchain` workflow from `main` when all
  break-glass gates are true:
  - `govulncheck -scan=module` or equivalent parsed JSON/SARIF evidence reports
    a standard-library or toolchain finding
  - the finding advertises a fixed Go patch for the current major.minor
  - go.dev publishes that fixed patch or a newer same-minor patch
  - live `nixpkgs-unstable` still lags behind the fixed patch
- The workflow updates only the Go override version/hash in `flake.nix`, runs
  `go mod tidy`, `govulncheck ./...`, `govulncheck -scan=module`,
  `nix flake check`, and `nix build`, then validates that the PR diff from
  `origin/main` contains only `flake.nix` before reusing the same updater
  branch/PR
- No-substitute source builds are manual attestation, not the default fast
  break-glass gate; dispatch the workflow with `source_attestation=true` or run
  `nix build --option substitute false --print-build-logs` explicitly when that
  evidence is needed
- Minor-version migrations still require manually updating the hard-coded
  `go126` / `go_1_26` names and then rerunning the alignment checks

When changing Go dependencies, `go.mod`, `go.sum`, Go major.minor versions, or
`vendorHash`:

- Run `go mod tidy`
- Run `nix build --option substitute false --print-build-logs`
- If Nix reports a `vendorHash` mismatch, copy the reported `got:` hash into
  `flake.nix` and rerun the build

## 2. Markdown Formatting

`markdown-formatter` covers all tracked Markdown files with its default
heading-numbering behavior enabled. Run `nix fmt` for treefmt-managed
formatting and let the pre-commit hook apply `mdfmt --write` to Markdown files.

The repository does not maintain separate root-doc or skill exceptions. Ignored
or generated files such as `.pre-commit-config.yaml` are not repository
Markdown policy surfaces.

## 3. Agent Skills

Validate publishable skill metadata with:

```sh
nix run '.#skill-check'
```

The local/CI skill check validates `skills/*/SKILL.md` frontmatter,
name-to-directory matching, license and description metadata, `USE FOR` /
`DO NOT USE FOR` discovery text, and then runs the GitHub skill publish
dry-run. `nix flake check` also runs the metadata validation through
pre-commit.

Do not use `gh skill publish --tag` in the tag-push release workflow. That
command creates a tag and GitHub Release itself. The repository release flow
uses the pushed `v*` tag plus GoReleaser, while `gh skill install` resolves
skills from the published repository tag or release.

## 4. Releases

See [RELEASING.md](RELEASING.md) for the release checklist and tag ruleset
expectations.

Agent-specific operating notes live in [CLAUDE.md](CLAUDE.md).
