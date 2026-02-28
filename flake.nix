{
  description = "tmux-a2a-postman - File-based communication daemon for tmux panes";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-25.11-darwin";
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

  outputs = inputs @ {
    self,
    flake-parts,
    git-hooks,
    treefmt-nix,
    ...
  }:
    flake-parts.lib.mkFlake {inherit inputs;} {
      systems = ["aarch64-darwin" "x86_64-linux" "aarch64-linux"];

      imports = [
        git-hooks.flakeModule
        treefmt-nix.flakeModule
      ];

      perSystem = {
        config,
        pkgs,
        ...
      }: let
        ghWorkflowFiles = "^\\.github/workflows/.*\\.(yml|yaml)$";
        rumdlConfig = pkgs.writeText "rumdl.toml" ''
          [global]
          disable = ["MD024"]

          [MD013]
          line-length = 120
        '';
        zizmorConfig = pkgs.writeText "zizmor.yml" ''
          rules:
            cache-poisoning:
              ignore:
                - release.yml
        '';
        version =
          if (builtins.hasAttr "ref" self && builtins.match "v[0-9]+\\.[0-9]+\\.[0-9]+" self.ref != null)
          then self.ref
          else if (builtins.hasAttr "shortRev" self)
          then "git-${self.shortRev}"
          else "dev";
        commit =
          if (builtins.hasAttr "rev" self)
          then (builtins.substring 0 7 self.rev)
          else "unknown";
      in {
        # nix build
        packages.default = pkgs.buildGoModule {
          pname = "tmux-a2a-postman";
          inherit version;
          src = ./.;
          vendorHash = "sha256-hobqsnqYUOVTb2cHWvr2wPtx4N2cpK2ciy30jCUyT6E=";
          go = pkgs.go_1_25;
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
            buildInputs = with pkgs; [go_1_25 gopls golangci-lint go-tools];
            shellHook = ''
              ${config.pre-commit.installationScript}
            '';
          };
          ci = pkgs.mkShell {
            buildInputs = with pkgs; [go_1_25 golangci-lint govulncheck];
          };
          cd = pkgs.mkShell {
            buildInputs = with pkgs; [go_1_25 goreleaser];
          };
        };

        # nix fmt
        treefmt = {
          projectRootFile = "flake.nix";
          settings.global.excludes = [".direnv" ".git" "*.lock"];
          programs = {
            gofumpt.enable = true;
            alejandra.enable = true;
          };
          settings.formatter.rumdl = {
            command = "${pkgs.rumdl}/bin/rumdl";
            options = ["fmt" "--config" "${rumdlConfig}"];
            includes = ["*.md"];
          };
        };

        # nix flake check (pre-commit hooks)
        pre-commit = {
          check.enable = true;
          settings.hooks = {
            # General
            end-of-file-fixer.enable = true;
            trim-trailing-whitespace.enable = true;
            check-added-large-files.enable = true;
            detect-private-keys.enable = true;
            check-merge-conflicts.enable = true;
            check-json.enable = true;
            check-yaml.enable = true;

            # Secrets
            gitleaks = {
              enable = true;
              entry = "${pkgs.gitleaks}/bin/gitleaks protect --verbose --redact --staged";
              pass_filenames = false;
            };

            # GitHub Actions
            actionlint.enable = true;
            ghalint = {
              enable = true;
              entry = "${pkgs.ghalint}/bin/ghalint run";
              files = ghWorkflowFiles;
              pass_filenames = false;
            };

            pinact = {
              enable = true;
              entry = "${pkgs.bash}/bin/bash -c 'test -n \"$NIX_BUILD_TOP\" && exit 0; ${pkgs.pinact}/bin/pinact run'";
              files = ghWorkflowFiles;
              pass_filenames = false;
            };
            zizmor = {
              enable = true;
              entry = "${pkgs.zizmor}/bin/zizmor --config ${zizmorConfig}";
              files = ghWorkflowFiles;
            };

            # Shell
            shellcheck.enable = true;

            # Nix
            statix = {
              enable = true;
              excludes = ["^\\.direnv/"];
            };
            flake-check = {
              enable = true;
              entry = "${pkgs.bash}/bin/bash -c 'test -n \"$NIX_BUILD_TOP\" || ${pkgs.nix}/bin/nix flake check'";
              pass_filenames = false;
              files = "\\.(nix|lock)$";
            };

            # Go
            govet = {
              enable = true;
              entry = "${pkgs.bash}/bin/bash -c 'test -n \"$NIX_BUILD_TOP\" && exit 0; ${pkgs.go_1_25}/bin/go vet ./...'";
              pass_filenames = false;
              types = ["go"];
            };

            # Markdown
            rumdl-check = {
              enable = true;
              entry = "${pkgs.rumdl}/bin/rumdl check --config ${rumdlConfig}";
              types = ["markdown"];
            };

            # Formatter
            treefmt = {
              enable = true;
              entry = "${pkgs.bash}/bin/bash -c 'test -n \"$NIX_BUILD_TOP\" || ${pkgs.nix}/bin/nix fmt'";
              pass_filenames = false;
              always_run = true;
            };
          };
        };
      };
    };
}
