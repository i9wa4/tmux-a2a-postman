#!/usr/bin/env bash

set -euo pipefail

# Exit codes:
#   0: no stdlib/toolchain vulnerabilities were found, or the Go toolchain
#      override was updated successfully.
#   1: a detection/update tool failed or a break-glass gate rejected the update.
version_ge() {
  first="$1"
  second="$2"
  [ "$(printf '%s\n%s\n' "$second" "$first" | sort -V | tail -n 1)" = "$first" ]
}

fail_gate() {
  gate="$1"
  reason="$2"
  echo "status=gate_failed"
  echo "gate=$gate"
  echo "reason=$reason"
  exit 1
}

go_mod_version="$(awk '/^go / { print $2; exit }' go.mod)"
go_minor="$(printf '%s\n' "$go_mod_version" | cut -d. -f1-2)"
if ! printf '%s\n' "$go_minor" | grep -Eq '^[0-9]+\.[0-9]+$'; then
  echo "Failed to extract Go major.minor from go.mod: $go_mod_version" >&2
  exit 1
fi

nix_minor="$(
  sed -nE 's/.*pkgs\.go_([0-9]+_[0-9]+).*/\1/p' flake.nix |
    head -n 1 |
    tr '_' '.'
)"
override_go="$(
  perl -0ne 'print "$1\n" if /pkgs\.go_[0-9]+_[0-9]+\.overrideAttrs \(_old: rec \{\n\s*version = "([0-9]+\.[0-9]+\.[0-9]+)";/' flake.nix
)"
if [ -z "$nix_minor" ]; then
  echo "Failed to extract Go major.minor from flake.nix" >&2
  exit 1
fi
if [ -z "$override_go" ]; then
  echo "Failed to extract Go override patch version from flake.nix" >&2
  exit 1
fi
if [ "$go_minor" != "$nix_minor" ]; then
  echo "Go major.minor mismatch: go.mod=$go_minor, flake.nix=$nix_minor" >&2
  exit 1
fi
case "$override_go" in
"$go_minor".*) ;;
*)
  echo "Go override mismatch: go.mod=$go_minor, override=$override_go" >&2
  exit 1
  ;;
esac

govuln_json="$(mktemp -t govulncheck-module.XXXXXX.jsonl)"
trap 'rm -f "$govuln_json"' EXIT

set +e
govulncheck -json -scan=module >"$govuln_json"
govuln_status=$?
set -e
if [ "$govuln_status" -ne 0 ] && [ "$govuln_status" -ne 3 ]; then
  echo "govulncheck -json -scan=module failed with status $govuln_status" >&2
  exit "$govuln_status"
fi

go_toolchain_findings="$(
  jq -r '
    select(.finding)
    | (
        [
          .finding.trace[]?
          | select(.module == "stdlib" or .module == "toolchain")
          | .module + "@" + (.version // "")
        ]
        | unique
      ) as $modules
    | select($modules | length > 0)
    | [
        .finding.osv,
        .finding.fixed_version,
        ($modules | join(","))
      ]
    | @tsv
  ' "$govuln_json" | sort -u
)"

if [ -z "$go_toolchain_findings" ]; then
  echo "no stdlib/toolchain vulnerabilities found"
  echo "status=clean"
  echo "go_minor=$go_minor"
  echo "current_go_version=$override_go"
  exit 0
fi

echo "status=findings_detected"
echo "go_minor=$go_minor"
echo "current_go_version=$override_go"
echo "findings<<FINDINGS"
printf '%s\n' "$go_toolchain_findings"
echo "FINDINGS"

fixed_versions="$(
  printf '%s\n' "$go_toolchain_findings" |
    awk -F '\t' -v prefix="$go_minor." '
        {
          fixed = $2
          sub(/^v/, "", fixed)
          sub(/^go/, "", fixed)
          if (fixed ~ "^" prefix "[0-9]+$") {
            print fixed
          }
        }
      ' |
    sort -V |
    uniq
)"
if [ -z "$fixed_versions" ]; then
  fail_gate "fixed_version" "stdlib/toolchain findings exist, but none advertise a fixed Go patch for $go_minor"
fi
target_go_version="$(printf '%s\n' "$fixed_versions" | tail -n 1)"
echo "target_go_version=$target_go_version"

latest="$(
  curl -fsSL 'https://go.dev/dl/?mode=json&include=all' |
    jq -r --arg prefix "go$go_minor." '
        [
          .[].version
          | select(startswith($prefix))
          | sub("^go"; "")
          | select(test("^[0-9]+\\.[0-9]+\\.[0-9]+$"))
        ]
        | .[]
      ' |
    sort -V |
    tail -n 1
)"
if [ -z "$latest" ]; then
  fail_gate "go_dev_release" "go.dev does not currently publish a stable Go $go_minor patch"
fi
if ! version_ge "$latest" "$target_go_version"; then
  fail_gate "go_dev_release" "latest upstream Go $latest does not satisfy fixed version $target_go_version"
fi
echo "upstream_go_version=$latest"

if version_ge "$override_go" "$target_go_version"; then
  fail_gate "current_override" "current flake.nix Go override $override_go already satisfies fixed version $target_go_version"
fi

go_version="$latest"
src_url="https://go.dev/dl/go$go_version.src.tar.gz"
hash="$(nix store prefetch-file --json "$src_url" | jq -r .hash)"
if [ -z "$hash" ] || [ "$hash" = "null" ]; then
  echo "Failed to prefetch $src_url" >&2
  exit 1
fi

GO_VERSION="$go_version" GO_HASH="$hash" perl -0pi -e '
  my $version = $ENV{"GO_VERSION"};
  my $hash = $ENV{"GO_HASH"};
  s/(go126 = pkgs\.go_1_26\.overrideAttrs \(_old: rec \{\n\s*version = ")[^"]+(";)/$1$version$2/
    or die "failed to update Go override version\n";
  s/(go126 = pkgs\.go_1_26\.overrideAttrs \(_old: rec \{.*?src = pkgs\.fetchurl \{\n\s*url = "https:\/\/go\.dev\/dl\/go\$\{version\}\.src\.tar\.gz";\n\s*hash = ")[^"]+(";)/$1$hash$2/s
    or die "failed to update Go override hash\n";
' flake.nix

echo "Updated Go toolchain override to $go_version"
echo "hash = $hash"
