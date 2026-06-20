#!/usr/bin/env bash

set -euo pipefail

version_gt() {
  first="$1"
  second="$2"
  [ "$first" != "$second" ] &&
    [ "$(printf '%s\n%s\n' "$second" "$first" | sort -V | tail -n 1)" = "$first" ]
}

override_go="$(
  perl -0ne 'print "$1\n" if /go126 = pkgs\.go_1_26\.overrideAttrs \(_old: rec \{\n\s*version = "([0-9]+\.[0-9]+\.[0-9]+)";/' flake.nix
)"
if [ -z "$override_go" ]; then
  echo "Failed to extract Go override patch version from flake.nix" >&2
  exit 1
fi

go_minor="$(printf '%s\n' "$override_go" | cut -d. -f1-2)"
if ! printf '%s\n' "$go_minor" | grep -Eq '^[0-9]+\.[0-9]+$'; then
  echo "Failed to extract Go major.minor from flake.nix override: $override_go" >&2
  exit 1
fi

latest="$(
  curl -fsSL 'https://go.dev/dl/?mode=json&include=all' |
    jq -r --arg prefix "go$go_minor." '
      [
        .[].version
        | select(startswith($prefix))
        | sub("^go"; "")
        | select(test("^[0-9]+\\.[0-9]+\\.[0-9]+$"))
      ]
      | sort_by(split(".") | map(tonumber))
      | last // empty
    '
)"
if [ -z "$latest" ]; then
  echo "go.dev does not currently publish a stable Go $go_minor patch" >&2
  exit 1
fi

echo "current_go_version=$override_go"
echo "latest_go_version=$latest"

if ! version_gt "$latest" "$override_go"; then
  echo "Go toolchain override is already at the latest Go $go_minor patch"
  echo "status=up_to_date"
  exit 0
fi

src_url="https://go.dev/dl/go$latest.src.tar.gz"
hash="$(nix store prefetch-file --json "$src_url" | jq -r .hash)"
if [ -z "$hash" ] || [ "$hash" = "null" ]; then
  echo "Failed to prefetch $src_url" >&2
  exit 1
fi

GO_VERSION="$latest" GO_HASH="$hash" perl -0pi -e '
  my $version = $ENV{"GO_VERSION"};
  my $hash = $ENV{"GO_HASH"};
  s/(go126 = pkgs\.go_1_26\.overrideAttrs \(_old: rec \{\n\s*version = ")[^"]+(";)/$1$version$2/
    or die "failed to update Go override version\n";
  s/(go126 = pkgs\.go_1_26\.overrideAttrs \(_old: rec \{.*?src = pkgs\.fetchurl \{\n\s*url = "https:\/\/go\.dev\/dl\/go\$\{version\}\.src\.tar\.gz";\n\s*hash = ")[^"]+(";)/$1$hash$2/s
    or die "failed to update Go override hash\n";
' flake.nix

echo "Updated Go toolchain override to $latest"
echo "hash=$hash"
echo "status=updated"
