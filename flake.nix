{
  description = "tmux-a2a-postman - File-based communication daemon for tmux panes";

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
          ...
        }:
        let
          ghWorkflowFiles = "^\\.github/workflows/.*\\.(yml|yaml)$";
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
          # nix build
          packages.default = pkgs.buildGoModule {
            pname = "tmux-a2a-postman";
            inherit version;
            src = ./.;
            vendorHash = "sha256-MFwOEIWCdMSmubtrvmjKKhHROnnq45GDqsO0jRZ27i4=";
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
              buildInputs = with pkgs; [
                go_1_25
                gopls
                golangci-lint
                go-tools
              ];
              shellHook = ''
                ${config.pre-commit.installationScript}
              '';
            };
            ci = pkgs.mkShell {
              buildInputs = with pkgs; [
                go_1_25
                gitleaks
                golangci-lint
                govulncheck
              ];
            };
            cd = pkgs.mkShell {
              buildInputs = with pkgs; [
                go_1_25
                goreleaser
              ];
            };
          };

          # nix fmt
          treefmt = {
            projectRootFile = "flake.nix";
            programs = {
              # Nix
              nixfmt.enable = true;
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

              # === Markdown linter ===
              rumdl-check = {
                enable = true;
                entry = "${pkgs.rumdl}/bin/rumdl check --config ${rumdlConfig}";
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
                entry = "${pkgs.bash}/bin/bash -c 'test -n \"$NIX_BUILD_TOP\" || ${pkgs.go_1_25}/bin/go vet ./...'";
                pass_filenames = false;
                types = [ "go" ];
              };
            };
          };
        };
    };
}
