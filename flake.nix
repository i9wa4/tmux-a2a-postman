{
  description = "tmux-a2a-postman - File-based communication daemon for tmux panes";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = {
    nixpkgs,
    flake-utils,
    ...
  }:
    flake-utils.lib.eachDefaultSystem (
      system: let
        pkgs = nixpkgs.legacyPackages.${system};
      in {
        packages.default = pkgs.buildGoModule {
          pname = "postman";
          version = "0.1.0";
          src = ./.;
          vendorHash = "sha256-qWQM+DlW/9/JlAv9M7QBUch3wSeOQFVgGxmkIm29yAM=";
        };
        devShells = {
          default = pkgs.mkShell {
            buildInputs = with pkgs; [
              go_1_24
              gopls
            ];
          };
          ci = pkgs.mkShell {
            buildInputs = with pkgs; [
              go_1_24
              golangci-lint
            ];
          };
        };
      }
    );
}
