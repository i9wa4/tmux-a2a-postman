{
  description = "tmux-a2a-postman - File-based communication daemon for tmux panes";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = {
    self,
    nixpkgs,
    flake-utils,
    ...
  }:
    flake-utils.lib.eachDefaultSystem (
      system: let
        pkgs = nixpkgs.legacyPackages.${system};

        # Version extraction strategy:
        # - Tagged releases (GitHub ?ref=v0.2.0): Use semantic version from tag
        # - Local clean builds: Use git commit hash (git-abc1234)
        # - Dirty working tree: Use "dev"
        # Note: self.ref only available for remote builds, not local
        version =
          # Tagged releases: Extract semantic version from git reference
          # (Only available when building from GitHub with ?ref=v0.2.0)
          if (builtins.hasAttr "ref" self && builtins.match "v[0-9]+\\.[0-9]+\\.[0-9]+" self.ref != null)
          then self.ref
          # Local/dev builds: Use 7-character commit hash
          # (self.shortRev available in clean working tree)
          else if (builtins.hasAttr "shortRev" self)
          then "git-${self.shortRev}"
          # Dirty working tree: Generic dev version
          else "dev";
        commit =
          if (builtins.hasAttr "rev" self)
          then (builtins.substring 0 7 self.rev)
          else "unknown";
      in {
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
        devShells = {
          default = pkgs.mkShell {
            buildInputs = with pkgs; [
              go_1_24
              gopls
              golangci-lint
            ];
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
      }
    );
}
