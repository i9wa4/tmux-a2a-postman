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

        # DEBUG: Print ALL self attributes for investigation
        debugInfo = builtins.trace "=== SELF ATTRIBUTES DEBUG ===" (
          builtins.trace "self.ref: ${if builtins.hasAttr "ref" self then self.ref else "UNDEFINED"}" (
          builtins.trace "self.rev: ${if builtins.hasAttr "rev" self then builtins.substring 0 12 self.rev else "UNDEFINED"}" (
          builtins.trace "self.shortRev: ${if builtins.hasAttr "shortRev" self then self.shortRev else "UNDEFINED"}" (
          builtins.trace "self.lastModified: ${if builtins.hasAttr "lastModified" self then toString self.lastModified else "UNDEFINED"}" (
          builtins.trace "self.narHash: ${if builtins.hasAttr "narHash" self then self.narHash else "UNDEFINED"}" (
          builtins.trace "Available attrs: ${builtins.toString (builtins.attrNames self)}" (
          builtins.trace "===========================" null
        )))))));

        version = builtins.seq debugInfo (builtins.replaceStrings ["\n"] [""] (builtins.readFile ./VERSION));
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
            "-X internal/version.Version=${version}"
            "-X internal/version.Commit=${commit}"
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
