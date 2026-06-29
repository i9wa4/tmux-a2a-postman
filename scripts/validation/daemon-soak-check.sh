#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
exec go run "$repo_root/scripts/validation/daemon_soak_check.go" "$@"
