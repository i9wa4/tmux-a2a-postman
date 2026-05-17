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
- Keep the `flake.nix` Go override on the latest required patch release until
  nixpkgs catches up
- Update the override with `nix run .#update-go-toolchain`, then run the checks
  below

When changing Go dependencies, `go.mod`, `go.sum`, Go major.minor versions, or
`vendorHash`:

- Run `go mod tidy`
- Run `nix build --option substitute false --print-build-logs`
- If Nix reports a `vendorHash` mismatch, copy the reported `got:` hash into
  `flake.nix` and rerun the build

## 2. Markdown Formatting

`docs/**/*.md` is long-form reference material and is formatted with heading
numbering enabled. Root public docs such as `README.md`, `CONTRIBUTING.md`, and
`RELEASING.md`, plus `skills/**/*.md`, are formatted with heading numbering
disabled so public anchors and skill section names stay stable.

## 3. Agent Skills

Validate publishable skill metadata with:

```sh
nix run '.#skill-check'
```

Do not use `gh skill publish --tag` in the tag-push release workflow. That
command creates a tag and GitHub Release itself. The repository release flow
uses the pushed `v*` tag plus GoReleaser, while `gh skill install` resolves
skills from the published repository tag or release.

## 4. Releases

See [RELEASING.md](RELEASING.md) for the release checklist and tag ruleset
expectations.

Agent-specific operating notes live in [CLAUDE.md](CLAUDE.md).
