{
  description = "tmux-a2a-postman - File-based communication daemon for tmux panes";

  nixConfig = {
    extra-substituters = [
      "https://nix-community.cachix.org"
      "https://cache.numtide.com"
    ];
    extra-trusted-public-keys = [
      "nix-community.cachix.org-1:mB9FSh9qf2dCimDSUo8Zy7bkq5CX+/rkCWyvRCYg3Fs="
      "niks3.numtide.com-1:DTx8wZduET09hRmMtKdQDxNNthLQETkc/yaX7M4qK0g="
    ];
  };

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-parts.url = "github:hercules-ci/flake-parts";
    git-hooks = {
      url = "github:cachix/git-hooks.nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    treefmt-nix = {
      url = "github:numtide/treefmt-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    markdown-formatter = {
      url = "github:i9wa4/markdown-formatter";
      inputs = {
        nixpkgs.follows = "nixpkgs";
        flake-parts.follows = "flake-parts";
        git-hooks.follows = "git-hooks";
        treefmt-nix.follows = "treefmt-nix";
      };
    };
  };

  outputs =
    inputs@{
      self,
      flake-parts,
      ...
    }:
    flake-parts.lib.mkFlake { inherit inputs; } {
      systems = [
        "aarch64-darwin"
        "x86_64-linux"
        "aarch64-linux"
      ];

      imports = [
        inputs.git-hooks.flakeModule
        inputs.treefmt-nix.flakeModule
      ];

      perSystem =
        {
          config,
          pkgs,
          system,
          ...
        }:
        let
          ghWorkflowFiles = "^\\.github/workflows/.*\\.(yml|yaml)$";
          go126 = pkgs.go_1_26.overrideAttrs (_old: rec {
            version = "1.26.4";
            src = pkgs.fetchurl {
              url = "https://go.dev/dl/go${version}.src.tar.gz";
              hash = "sha256-T2aKMvv8ETLmqIH7lowvHa2mMUkqM5IRc1+7JVpCYC0=";
            };
          });
          buildGo126Module = pkgs.buildGoModule.override { go = go126; };
          commonDevPackages = with pkgs; [
            gh
            go126
          ];
          goDevPackages = with pkgs; [
            gopls
            golangci-lint
            go-tools
          ];
          ciPackages = with pkgs; [
            gitleaks
            golangci-lint
            govulncheck
          ];
          cdPackages = with pkgs; [
            goreleaser
          ];
          markdownFormatter = "${inputs.markdown-formatter.packages.${system}.default}/bin/mdfmt";
          skillMetadataCheck = "${pkgs.bash}/bin/bash scripts/validation/validate-skill-metadata.sh skills";
          rumdlConfig = pkgs.writeText "rumdl.toml" ''
            [MD013]
            code-blocks = false
            headings = false
            reflow = true
          '';
          zizmorConfig = pkgs.writeText "zizmor.yml" ''
            rules:
              cache-poisoning:
                ignore:
                  - release.yml
          '';
          version =
            if (builtins.hasAttr "ref" self && builtins.match "v[0-9]+\\.[0-9]+\\.[0-9]+" self.ref != null) then
              self.ref
            else if (builtins.hasAttr "shortRev" self) then
              "git-${self.shortRev}"
            else
              "dev";
          commit = if (builtins.hasAttr "rev" self) then (builtins.substring 0 7 self.rev) else "unknown";
        in
        {
          apps = {
            update = {
              type = "app";
              program = "${pkgs.writeShellScriptBin "update" ''
                set -euo pipefail
                exec ${pkgs.nix}/bin/nix flake update "$@"
              ''}/bin/update";
              meta.description = "Update flake inputs.";
            };

            update-go-toolchain = {
              type = "app";
              program = "${pkgs.writeShellScriptBin "update-go-toolchain" ''
                set -euo pipefail
                export PATH="${go126}/bin:${pkgs.git}/bin:$PATH"

                # Exit codes:
                #   0: no stdlib/toolchain vulnerabilities were found, or the
                #      Go toolchain override was updated successfully.
                #   1: a detection/update tool failed or a break-glass gate
                #      rejected the update.
                version_ge() {
                  first="$1"
                  second="$2"
                  [ "$(printf '%s\n%s\n' "$second" "$first" | ${pkgs.coreutils}/bin/sort -V | ${pkgs.coreutils}/bin/tail -n 1)" = "$first" ]
                }

                fail_gate() {
                  gate="$1"
                  reason="$2"
                  echo "status=gate_failed"
                  echo "gate=$gate"
                  echo "reason=$reason"
                  exit 1
                }

                go_mod_version="$(${pkgs.gawk}/bin/awk '/^go / { print $2; exit }' go.mod)"
                go_minor="$(printf '%s\n' "$go_mod_version" | ${pkgs.coreutils}/bin/cut -d. -f1-2)"
                if ! printf '%s\n' "$go_minor" | ${pkgs.gnugrep}/bin/grep -Eq '^[0-9]+\.[0-9]+$'; then
                  echo "Failed to extract Go major.minor from go.mod: $go_mod_version" >&2
                  exit 1
                fi

                nix_minor="$(
                  ${pkgs.gnused}/bin/sed -nE 's/.*pkgs\.go_([0-9]+_[0-9]+).*/\1/p' flake.nix \
                    | ${pkgs.coreutils}/bin/head -n 1 \
                    | ${pkgs.coreutils}/bin/tr '_' '.'
                )"
                override_go="$(
                  ${pkgs.perl}/bin/perl -0ne 'print "$1\n" if /pkgs\.go_[0-9]+_[0-9]+\.overrideAttrs \(_old: rec \{\n\s*version = "([0-9]+\.[0-9]+\.[0-9]+)";/' flake.nix
                )"
                if [ -z "$nix_minor" ]; then
                  echo "Failed to extract Go major.minor from flake.nix" >&2
                  exit 1
                fi
                if [ -z "$override_go" ]; then
                  echo "Failed to extract Go override patch version from flake.nix" >&2
                  exit 1
                fi
                if [ "$go_minor" != "$nix_minor" ]; then
                  echo "Go major.minor mismatch: go.mod=$go_minor, flake.nix=$nix_minor" >&2
                  exit 1
                fi
                case "$override_go" in
                  "$go_minor".*) ;;
                  *)
                    echo "Go override mismatch: go.mod=$go_minor, override=$override_go" >&2
                    exit 1
                    ;;
                esac

                govuln_json="$(${pkgs.coreutils}/bin/mktemp -t govulncheck-module.XXXXXX.jsonl)"
                trap 'rm -f "$govuln_json"' EXIT

                set +e
                ${pkgs.govulncheck}/bin/govulncheck -json -scan=module >"$govuln_json"
                govuln_status=$?
                set -e
                if [ "$govuln_status" -ne 0 ] && [ "$govuln_status" -ne 3 ]; then
                  echo "govulncheck -json -scan=module failed with status $govuln_status" >&2
                  exit "$govuln_status"
                fi

                go_toolchain_findings="$(
                  ${pkgs.jq}/bin/jq -r '
                    select(.finding)
                    | (
                        [
                          .finding.trace[]?
                          | select(.module == "stdlib" or .module == "toolchain")
                          | .module + "@" + (.version // "")
                        ]
                        | unique
                      ) as $modules
                    | select($modules | length > 0)
                    | [
                        .finding.osv,
                        .finding.fixed_version,
                        ($modules | join(","))
                      ]
                    | @tsv
                  ' "$govuln_json" | ${pkgs.coreutils}/bin/sort -u
                )"

                if [ -z "$go_toolchain_findings" ]; then
                  echo "no stdlib/toolchain vulnerabilities found"
                  echo "status=clean"
                  echo "go_minor=$go_minor"
                  echo "current_go_version=$override_go"
                  exit 0
                fi

                echo "status=findings_detected"
                echo "go_minor=$go_minor"
                echo "current_go_version=$override_go"
                echo "findings<<FINDINGS"
                printf '%s\n' "$go_toolchain_findings"
                echo "FINDINGS"

                fixed_versions="$(
                  printf '%s\n' "$go_toolchain_findings" \
                    | ${pkgs.gawk}/bin/awk -F '\t' -v prefix="$go_minor." '
                        {
                          fixed = $2
                          sub(/^v/, "", fixed)
                          sub(/^go/, "", fixed)
                          if (fixed ~ "^" prefix "[0-9]+$") {
                            print fixed
                          }
                        }
                      ' \
                    | ${pkgs.coreutils}/bin/sort -V \
                    | ${pkgs.coreutils}/bin/uniq
                )"
                if [ -z "$fixed_versions" ]; then
                  fail_gate "fixed_version" "stdlib/toolchain findings exist, but none advertise a fixed Go patch for $go_minor"
                fi
                target_go_version="$(printf '%s\n' "$fixed_versions" | ${pkgs.coreutils}/bin/tail -n 1)"
                echo "target_go_version=$target_go_version"

                latest="$(
                  ${pkgs.curl}/bin/curl -fsSL 'https://go.dev/dl/?mode=json&include=all' \
                    | ${pkgs.jq}/bin/jq -r --arg prefix "go$go_minor." '
                        [
                          .[].version
                          | select(startswith($prefix))
                          | sub("^go"; "")
                          | select(test("^[0-9]+\\.[0-9]+\\.[0-9]+$"))
                        ]
                        | .[]
                      ' \
                    | ${pkgs.coreutils}/bin/sort -V \
                    | ${pkgs.coreutils}/bin/tail -n 1
                )"
                if [ -z "$latest" ]; then
                  fail_gate "go_dev_release" "go.dev does not currently publish a stable Go $go_minor patch"
                fi
                if ! version_ge "$latest" "$target_go_version"; then
                  fail_gate "go_dev_release" "latest upstream Go $latest does not satisfy fixed version $target_go_version"
                fi
                echo "upstream_go_version=$latest"

                if version_ge "$override_go" "$target_go_version"; then
                  fail_gate "current_override" "current flake.nix Go override $override_go already satisfies fixed version $target_go_version"
                fi

                go_version="$latest"
                src_url="https://go.dev/dl/go$go_version.src.tar.gz"
                hash="$(
                  ${pkgs.nix}/bin/nix store prefetch-file --json "$src_url" \
                    | ${pkgs.jq}/bin/jq -r .hash
                )"
                if [ -z "$hash" ] || [ "$hash" = "null" ]; then
                  echo "Failed to prefetch $src_url" >&2
                  exit 1
                fi

                GO_VERSION="$go_version" GO_HASH="$hash" ${pkgs.perl}/bin/perl -0pi -e '
                  my $version = $ENV{"GO_VERSION"};
                  my $hash = $ENV{"GO_HASH"};
                  s/(go126 = pkgs\.go_1_26\.overrideAttrs \(_old: rec \{\n\s*version = ")[^"]+(";)/$1$version$2/
                    or die "failed to update Go override version\n";
                  s/(go126 = pkgs\.go_1_26\.overrideAttrs \(_old: rec \{.*?src = pkgs\.fetchurl \{\n\s*url = "https:\/\/go\.dev\/dl\/go\$\{version\}\.src\.tar\.gz";\n\s*hash = ")[^"]+(";)/$1$hash$2/s
                    or die "failed to update Go override hash\n";
                ' flake.nix

                echo "Updated Go toolchain override to $go_version"
                echo "hash = $hash"
              ''}/bin/update-go-toolchain";
              meta.description = "Detect Go stdlib/toolchain vulnerabilities and update the pinned Go patch override when needed.";
            };

            check = {
              type = "app";
              program = "${pkgs.writeShellScriptBin "check" ''
                set -euo pipefail
                exec ${pkgs.nix}/bin/nix flake check --print-build-logs "$@"
              ''}/bin/check";
              meta.description = "Run the flake checks.";
            };

            skill-check = {
              type = "app";
              program = "${pkgs.writeShellScriptBin "skill-check" ''
                set -euo pipefail
                ${skillMetadataCheck}
                exec ${pkgs.gh}/bin/gh skill publish --dry-run "$@"
              ''}/bin/skill-check";
              meta.description = "Validate agent skill metadata and publish packaging without publishing.";
            };

            skill-publish = {
              type = "app";
              program = "${pkgs.writeShellScriptBin "skill-publish" ''
                set -euo pipefail
                exec ${pkgs.gh}/bin/gh skill publish "$@"
              ''}/bin/skill-publish";
              meta.description = "Publish agent skills with GitHub CLI.";
            };
          };

          # nix build
          packages.default = buildGo126Module {
            pname = "tmux-a2a-postman";
            inherit version;
            src = ./.;
            vendorHash = "sha256-/G6E+StNyCnZeb1ZgdiyK6D8pDLX+2YTlJrCEv74lbQ=";
            ldflags = [
              "-s"
              "-w"
              "-X github.com/i9wa4/tmux-a2a-postman/internal/version.Version=${version}"
              "-X github.com/i9wa4/tmux-a2a-postman/internal/version.Commit=${commit}"
            ];
          };

          # nix develop
          devShells = {
            default = pkgs.mkShell {
              buildInputs = commonDevPackages ++ goDevPackages;
              shellHook = ''
                ${config.pre-commit.installationScript}
              '';
            };
            ci = pkgs.mkShell {
              buildInputs = commonDevPackages ++ ciPackages;
            };
            cd = pkgs.mkShell {
              buildInputs = commonDevPackages ++ cdPackages;
            };
          };

          # nix fmt
          treefmt = {
            projectRootFile = "flake.nix";
            programs = {
              # Nix
              nixfmt.enable = true;
              # Shell
              shfmt = {
                enable = true;
                indent_size = 2;
              };
              # Go
              gofumpt.enable = true;
            };
            settings = {
              formatter = {
                # Markdown
                rumdl = {
                  command = "${pkgs.rumdl}/bin/rumdl";
                  options = [
                    "fmt"
                    "--config"
                    "${rumdlConfig}"
                  ];
                  includes = [ "*.md" ];
                };
                # JSON
                jq = {
                  command = "${pkgs.jq}/bin/jq";
                  options = [ "." ];
                  includes = [ "*.json" ];
                };
              };
              global.excludes = [
                ".direnv"
                ".git"
                "*.lock"
              ];
            };
          };

          # nix flake check (pre-commit hooks)
          pre-commit = {
            check.enable = true;
            settings.hooks = {
              # === General file checks ===
              end-of-file-fixer.enable = true;
              trim-trailing-whitespace.enable = true;
              check-added-large-files.enable = true;
              detect-private-keys.enable = true;
              check-merge-conflicts.enable = true;
              check-json.enable = true;
              check-yaml.enable = true;

              # === Secrets detection ===
              gitleaks = {
                enable = true;
                entry = "${pkgs.gitleaks}/bin/gitleaks protect --verbose --redact --staged";
                pass_filenames = false;
              };

              # === GitHub Actions linters ===
              actionlint.enable = true;

              ghalint = {
                enable = true;
                entry = "${pkgs.ghalint}/bin/ghalint run";
                files = ghWorkflowFiles;
              };

              pinact = {
                enable = true;
                entry = "${pkgs.pinact}/bin/pinact run";
                files = ghWorkflowFiles;
              };

              zizmor = {
                enable = true;
                entry = "${pkgs.zizmor}/bin/zizmor --config ${zizmorConfig}";
                files = ghWorkflowFiles;
              };

              # === Nix linter ===
              statix = {
                enable = true;
                entry = "${pkgs.bash}/bin/bash -c '${pkgs.statix}/bin/statix check flake.nix'";
                pass_filenames = false;
              };
              deadnix.enable = true;

              # === Markdown linter ===
              rumdl-check = {
                enable = true;
                entry = "${pkgs.rumdl}/bin/rumdl check --config ${rumdlConfig}";
                types = [ "markdown" ];
              };
              # Apply markdown-formatter uniformly to repository Markdown.
              # Generated hook config is ignored output and not a policy surface.
              markdown-formatter = {
                enable = true;
                name = "markdown-formatter (all tracked markdown)";
                entry = "${markdownFormatter} --write";
                types = [ "markdown" ];
              };

              # === Agent skills ===
              skill-metadata = {
                enable = true;
                name = "validate agent skill metadata";
                entry = skillMetadataCheck;
                files = "^skills/[^/]+/SKILL\\.md$";
                pass_filenames = false;
              };

              # === Shell ===
              shellcheck.enable = true;

              # === Unified formatter ===
              # Skip in sandbox (treefmt-nix already runs treefmt-check separately)
              treefmt = {
                enable = true;
                entry = "${pkgs.bash}/bin/bash -c 'test -n \"$NIX_BUILD_TOP\" || ${pkgs.nix}/bin/nix fmt'";
                pass_filenames = false;
                always_run = true;
              };

              # === Language-specific linters ===
              # Go
              govet = {
                enable = true;
                entry = "${pkgs.bash}/bin/bash -c 'test -n \"$NIX_BUILD_TOP\" || ${go126}/bin/go vet ./...'";
                pass_filenames = false;
                types = [ "go" ];
              };
            };
          };
        };
    };
}
