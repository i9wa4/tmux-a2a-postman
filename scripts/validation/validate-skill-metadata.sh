#!/usr/bin/env bash
# Validate repository-owned Agent Skill frontmatter and discovery metadata.

set -euo pipefail

usage() {
  echo "usage: validate-skill-metadata.sh <skill-root>" >&2
}

if [ "$#" -ne 1 ]; then
  usage
  exit 64
fi

root=$1
if [ ! -d "$root" ]; then
  echo "FAIL: skill root not found: $root" >&2
  exit 1
fi

failures=0

fail() {
  failures=$((failures + 1))
  printf 'FAIL: %s: %s\n' "$1" "$2" >&2
}

frontmatter_value() {
  local key=$1
  local file=$2

  awk -v key="$key" '
    BEGIN {
      state = "need_open"
      collecting = 0
      value = ""
    }
    state == "need_open" {
      if ($0 == "---") {
        state = "frontmatter"
      }
      next
    }
    state == "frontmatter" {
      if ($0 == "---") {
        print value
        exit
      }
      if (collecting) {
        if ($0 ~ /^[[:space:]]+/) {
          line = $0
          sub(/^[[:space:]]+/, "", line)
          if (value != "") {
            value = value " "
          }
          value = value line
          next
        }
        collecting = 0
      }
      if ($0 ~ ("^" key ":[[:space:]]*[>|]")) {
        collecting = 1
        next
      }
      if ($0 ~ ("^" key ":[[:space:]]*")) {
        line = $0
        sub("^" key ":[[:space:]]*", "", line)
        print line
        exit
      }
      next
    }
  ' "$file"
}

body_line_count() {
  awk '
    BEGIN {
      state = "need_open"
      lines = 0
    }
    state == "need_open" {
      if ($0 == "---") {
        state = "frontmatter"
      }
      next
    }
    state == "frontmatter" {
      if ($0 == "---") {
        state = "body"
      }
      next
    }
    state == "body" && $0 !~ /^[[:space:]]*$/ {
      lines++
    }
    END {
      print lines
    }
  ' "$1"
}

while IFS= read -r skill; do
  skill_dir=$(basename "$(dirname "$skill")")

  if ! head -n 1 "$skill" | grep -qx -- "---"; then
    fail "$skill" "first line must be frontmatter opener ---"
    continue
  fi

  name=$(frontmatter_value name "$skill")
  license=$(frontmatter_value license "$skill")
  description=$(frontmatter_value description "$skill")
  body_lines=$(body_line_count "$skill")

  if [ -z "$name" ]; then
    fail "$skill" "frontmatter missing name"
  elif [ "$name" != "$skill_dir" ]; then
    fail "$skill" "frontmatter name '$name' must match directory '$skill_dir'"
  fi

  if [ -z "$license" ]; then
    fail "$skill" "frontmatter missing license"
  fi

  if [ -z "$description" ]; then
    fail "$skill" "frontmatter missing description"
  else
    case "$description" in
    *"USE FOR:"*) ;;
    *)
      fail "$skill" "description must include USE FOR: discovery text"
      ;;
    esac
    case "$description" in
    *"DO NOT USE FOR:"*) ;;
    *)
      fail "$skill" "description must include DO NOT USE FOR: boundary text"
      ;;
    esac
  fi

  if [ "$body_lines" -eq 0 ]; then
    fail "$skill" "body must not be empty"
  fi
done < <(find -L "$root" -mindepth 2 -maxdepth 2 -name SKILL.md -print | sort)

if [ "$failures" -gt 0 ]; then
  exit 1
fi

echo "skill metadata validation: OK"
