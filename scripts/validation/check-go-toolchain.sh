#!/usr/bin/env bash
set -euo pipefail

module_go_version=""
while read -r keyword value _module_remainder; do
  if [[ $keyword == go ]]; then
    module_go_version=$value
    break
  fi
done <go.mod

if [[ ! $module_go_version =~ ^([0-9]+)\.([0-9]+)(\.[0-9]+)?$ ]]; then
  printf 'Cannot read the Go language version from go.mod\n' >&2
  exit 1
fi
module_minor="${BASH_REMATCH[1]}.${BASH_REMATCH[2]}"
selected_go_version="$(GOTOOLCHAIN=local go env GOVERSION)"
if [[ ! $selected_go_version =~ ^go([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
  printf 'Expected a selected stable Go toolchain, got %s\n' "$selected_go_version" >&2
  exit 1
fi
selected_minor="${BASH_REMATCH[1]}.${BASH_REMATCH[2]}"
printf 'go.mod language: %s\nSelected Go toolchain: %s\n' "$module_go_version" "$selected_go_version"
if [[ $module_minor != "$selected_minor" ]]; then
  printf 'Go major.minor mismatch: module=%s selected=%s\n' "$module_minor" "$selected_minor" >&2
  exit 1
fi
