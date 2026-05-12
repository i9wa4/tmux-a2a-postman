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
            version = "1.26.3";
            src = pkgs.fetchurl {
              url = "https://go.dev/dl/go${version}.src.tar.gz";
              hash = "sha256-HGRoddCqh5kTMYTtV895/yS97+jIggRwYCqdPW2Rkrg=";
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

                go_mod_version="$(awk '/^go / { print $2; exit }' go.mod)"
                go_minor="$(printf '%s\n' "$go_mod_version" | cut -d. -f1-2)"
                if ! printf '%s\n' "$go_minor" | grep -Eq '^[0-9]+\.[0-9]+$'; then
                  echo "Failed to extract Go major.minor from go.mod: $go_mod_version" >&2
                  exit 1
                fi

                nix_minor="$(
                  ${pkgs.gnused}/bin/sed -nE 's/.*pkgs\.go_([0-9]+_[0-9]+).*/\1/p' flake.nix \
                    | head -n 1 \
                    | tr '_' '.'
                )"
                if [ "$go_minor" != "$nix_minor" ]; then
                  echo "Go major.minor mismatch: go.mod=$go_minor, flake.nix=$nix_minor" >&2
                  exit 1
                fi

                latest="$(
                  ${pkgs.curl}/bin/curl -fsSL 'https://go.dev/dl/?mode=json' \
                    | ${pkgs.jq}/bin/jq -r --arg prefix "go$go_minor." '[.[].version | select(startswith($prefix))][0]'
                )"
                if [ -z "$latest" ] || [ "$latest" = "null" ]; then
                  echo "Failed to find latest Go patch for $go_minor" >&2
                  exit 1
                fi

                go_version="''${latest#go}"
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
              meta.description = "Update the pinned Go patch override and source hash.";
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
                exec ${pkgs.gh}/bin/gh skill publish --dry-run "$@"
              ''}/bin/skill-check";
              meta.description = "Validate agent skills without publishing.";
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
            vendorHash = "sha256-M+3UmTcpX6lvGOtsJSjd1WLwO8lM4YYOJaHwrAW1eQc=";
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
              markdown-formatter = {
                enable = true;
                name = "markdown-formatter";
                entry = "${markdownFormatter} --no-heading-numbering --write";
                types = [ "markdown" ];
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
