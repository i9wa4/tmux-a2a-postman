.PHONY: check build verify

check:
	nix flake check

build:
	nix build

verify: check build
