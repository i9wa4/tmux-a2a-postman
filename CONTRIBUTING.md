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
- Keep the `flake.nix` Go override on the latest stable same-minor patch release
  published by go.dev
- To update the Go patch override, run:

  ```sh
  nix run .#update-go-toolchain
  ```

- The command reads the current Go major.minor from the `go126` override in
  `flake.nix`, queries go.dev for the latest stable patch release for that same
  major.minor, and updates only the override version/hash in `flake.nix` when a
  newer patch exists.
- If the override is already current, the command exits 0 with
  `status=up_to_date` and leaves the repository unchanged.
- After the command updates `flake.nix`, run `nix flake check` and `nix build`
  before opening the update PR.
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
