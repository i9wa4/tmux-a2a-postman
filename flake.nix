{
  description = "tmux-a2a-postman - Daemonless file-first communication for tmux agents";

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
          goToolchain = pkgs.go_1_26;
          buildGoModuleWithToolchain = pkgs.buildGoModule.override { go = goToolchain; };
          waza = pkgs.callPackage ./nix/packages/waza.nix { inherit system; };
          triggerPython = pkgs.python3.withPackages (ps: [ ps.pyyaml ]);
          triggerSource = pkgs.lib.fileset.toSource {
            root = ./.;
            fileset = pkgs.lib.fileset.unions [
              ./skills
              ./evals
              ./scripts/validation/validate-skill-triggers.py
              ./scripts/validation/test-skill-triggers.py
            ];
          };
          skillTriggerCheck = pkgs.writeShellApplication {
            name = "skill-trigger-check";
            runtimeInputs = [
              triggerPython
              waza
            ];
            text = ''
              exec python3 ${./scripts/validation/validate-skill-triggers.py} \
                --root "$PWD" --waza ${waza}/bin/waza "$@"
            '';
          };
          recursiveSource = pkgs.lib.fileset.toSource {
            root = ./.;
            fileset = pkgs.lib.fileset.unions [
              ./scripts/validation
              ./skills
            ];
          };
          recursiveSmoke = pkgs.writeShellApplication {
            name = "recursive-smoke";
            runtimeInputs = [
              pkgs.python3
              pkgs.bash
              pkgs.tmux
              pkgs.git
              pkgs.coreutils
            ];
            text = ''
              exec python3 ${recursiveSource}/scripts/validation/recursive-smoke.py \
                --postman ${config.packages.default}/bin/tmux-a2a-postman "$@"
            '';
          };
          commonDevPackages =
            with pkgs;
            [
              gh
              goToolchain
            ]
            ++ [
              waza
              triggerPython
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
          skillMetadataCheck = "${pkgs.coreutils}/bin/env PATH=${
            pkgs.lib.makeBinPath [
              pkgs.gawk
              pkgs.coreutils
              pkgs.findutils
              pkgs.gnugrep
            ]
          } ${pkgs.bash}/bin/bash scripts/validation/validate-skill-metadata.sh skills";
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
            recursive-smoke = {
              type = "app";
              program = "${recursiveSmoke}/bin/recursive-smoke";
              meta.description = "Verify recursive mail with isolated deterministic terminal teams.";
            };
            skill-trigger-check = {
              type = "app";
              program = "${skillTriggerCheck}/bin/skill-trigger-check";
              meta.description = "Validate and run all offline mock skill-trigger suites.";
            };
            notification-smoke = {
              type = "app";
              program = "${
                pkgs.writeShellApplication {
                  name = "notification-smoke";
                  runtimeInputs = [
                    pkgs.bash
                    pkgs.python3
                    pkgs.tmux
                  ];
                  text = ''
                    exec python3 ${./scripts/validation}/notification-smoke.py \
                      --postman ${config.packages.default}/bin/tmux-a2a-postman \
                      "$@"
                  '';
                }
              }/bin/notification-smoke";
              meta.description = "Test generic tmux input notifications with deterministic terminal agents.";
            };

            update = {
              type = "app";
              program = "${pkgs.writeShellScriptBin "update" ''
                set -euo pipefail
                exec ${pkgs.nix}/bin/nix flake update "$@"
              ''}/bin/update";
              meta.description = "Update flake inputs.";
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
          packages.waza = waza;
          packages.default = buildGoModuleWithToolchain {
            pname = "tmux-a2a-postman";
            inherit version;
            src = ./.;
            vendorHash = "sha256-wkLAGzVge9WOZdLKPB0cuW1cpzy/rXKTTmqj29uqFv0=";
            nativeCheckInputs = [
              pkgs.tmux
              pkgs.bash
              pkgs.zsh
              pkgs.direnv
              pkgs.python3
            ];
            ldflags = [
              "-s"
              "-w"
              "-X github.com/i9wa4/tmux-a2a-postman/internal/version.Version=${version}"
              "-X github.com/i9wa4/tmux-a2a-postman/internal/version.Commit=${commit}"
            ];
          };

          # nix develop
          checks = {
            notification = config.packages.default;
            recursive-teams = pkgs.runCommand "recursive-teams" { nativeBuildInputs = [ pkgs.python3 ]; } ''
              export HOME="$TMPDIR/home"
              mkdir -p "$HOME"
              python3 ${recursiveSource}/scripts/validation/test-recursive-configuration.py
              ${recursiveSmoke}/bin/recursive-smoke --output "$TMPDIR/results.json"
              mkdir -p "$out"
              cp "$TMPDIR/results.json" "$out/results.json"
            '';
            retired-task-artifacts = pkgs.runCommand "retired-task-artifacts" { } ''
              if test -e ${./.}/.task-artifacts; then
                echo 'Retired task artifacts must not be tracked; see docs/validation/740-retired-task-artifacts.md' >&2
                exit 1
              fi
              touch "$out"
            '';
            status-benchmark =
              pkgs.runCommand "status-benchmark"
                {
                  nativeBuildInputs = [
                    pkgs.python3
                    pkgs.tmux
                  ];
                }
                ''
                  export HOME="$TMPDIR/home"
                  mkdir -p "$HOME" "$out"
                  python3 ${./scripts/validation/benchmark-invocations.py} \
                    --candidate ${config.packages.default}/bin/tmux-a2a-postman \
                    --samples 3 --history 10 --concurrency 4 > "$out/results.json"
                  python3 - "$out/results.json" <<'PYTHON'
                  import json, sys
                  result = json.load(open(sys.argv[1]))["candidate"]
                  assert result["supports_refresh"] and result["incomplete_logs"] == 0
                  assert result["raw_capture_files"] > 0
                  print(json.dumps(result, indent=2))
                  PYTHON
                '';
            invocation-analysis =
              pkgs.runCommand "invocation-analysis" { nativeBuildInputs = [ pkgs.python3 ]; }
                ''
                  export POSTMAN_DIAGNOSTIC_ANALYZER=${./scripts/validation/analyze-invocations.py}
                  python3 ${./scripts/validation/test-invocation-analysis.py}
                  touch "$out"
                '';
            skill-team-manager =
              pkgs.runCommand "skill-team-manager" { nativeBuildInputs = [ pkgs.python3 ]; }
                ''
                  export POSTMAN_MANAGER_SKILL_ROOT=${./skills/postman-team-manager}
                  python3 ${./scripts/validation/test-skill-team-manager.py}
                  touch "$out"
                '';
            skill-recall = pkgs.runCommand "skill-recall" { nativeBuildInputs = [ pkgs.python3 ]; } ''
              export POSTMAN_RECALL_SKILL_ROOT=${./skills/postman-recall}
              python3 ${./scripts/validation/test-skill-recall.py}
              touch "$out"
            '';
            skill-triggers =
              pkgs.runCommand "skill-triggers"
                {
                  nativeBuildInputs = [
                    triggerPython
                    waza
                  ];
                }
                ''
                  export HOME="$TMPDIR/home"
                  mkdir -p "$HOME"
                  export WAZA_BIN=${waza}/bin/waza
                  python3 ${triggerSource}/scripts/validation/test-skill-triggers.py
                  python3 ${triggerSource}/scripts/validation/validate-skill-triggers.py --root ${triggerSource}
                  touch "$out"
                '';
          };
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

              go-toolchain = {
                enable = true;
                name = "check selected Go toolchain";
                entry = "${pkgs.coreutils}/bin/env PATH=${
                  pkgs.lib.makeBinPath [ goToolchain ]
                } ${pkgs.bash}/bin/bash scripts/validation/check-go-toolchain.sh";
                files = "^(go\\.mod|flake\\.(nix|lock)|scripts/validation/check-go-toolchain\\.sh)$";
                pass_filenames = false;
              };

              # === Agent skills ===
              skill-metadata = {
                enable = true;
                name = "validate agent skill metadata";
                entry = skillMetadataCheck;
                files = "^skills/[^/]+/SKILL\\.md$";
                pass_filenames = false;
              };
              skill-triggers = {
                enable = true;
                name = "deterministic skill triggers";
                entry = "${skillTriggerCheck}/bin/skill-trigger-check";
                files = "^(skills/|evals/[^/]+/trigger/)";
                pass_filenames = false;
                require_serial = true;
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
                entry = "${pkgs.bash}/bin/bash -c 'test -n \"$NIX_BUILD_TOP\" || ${goToolchain}/bin/go vet ./...'";
                pass_filenames = false;
                types = [ "go" ];
              };
            };
          };
        };
    };
}
