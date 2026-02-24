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

  outputs = inputs @ {
    self,
    flake-parts,
    git-hooks,
    treefmt-nix,
    ...
  }:
    flake-parts.lib.mkFlake {inherit inputs;} {
      systems = ["x86_64-darwin" "aarch64-darwin" "x86_64-linux" "aarch64-linux"];

      imports = [
        git-hooks.flakeModule
        treefmt-nix.flakeModule
      ];

      perSystem = {
        config,
        pkgs,
        ...
      }: let
        # Version extraction strategy:
        # - Tagged releases (GitHub ?ref=v0.2.0): Use semantic version from tag
        # - Local clean builds: Use git commit hash (git-abc1234)
        # - Dirty working tree: Use "dev"
        # Note: self.ref only available for remote builds, not local
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
          vendorHash = "sha256-Bd3OE7lsEwUrDtpHWCqbMfhaDiaXRDxwvsJd/XGi+Pc=";
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
              go_1_24
              gopls
              golangci-lint
            ];
            shellHook = ''
              ${config.pre-commit.installationScript}
            '';
          };
          ci = pkgs.mkShell {
            buildInputs = with pkgs; [
              go_1_24
              golangci-lint
              govulncheck
            ];
          };
          cd = pkgs.mkShell {
            buildInputs = with pkgs; [
              go_1_24
              goreleaser
            ];
          };
        };

        # nix fmt
        treefmt = {
          projectRootFile = "flake.nix";
          programs = {
            gofumpt.enable = true;
            alejandra.enable = true;
          };
        };

        # nix flake check (pre-commit hooks)
        pre-commit = {
          check.enable = true;
          settings.hooks = {
            gitleaks = {
              enable = true;
              entry = "${pkgs.gitleaks}/bin/gitleaks protect --verbose --redact --staged";
              pass_filenames = false;
            };
            actionlint.enable = true;
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
