#!/usr/bin/env python3
"""Generate thin installable Skills from canonical Skill frontmatter."""

import argparse
from pathlib import Path
import sys

import yaml


def generate(root, check=False):
    expected = {}
    for source in sorted((root / "skills").glob("*/SKILL.md")):
        text = source.read_text(encoding="utf-8")
        if not text.startswith("---\n") or "\n---\n" not in text[4:]:
            raise ValueError(f"{source}: expected YAML frontmatter")
        header, _ = text[4:].split("\n---\n", 1)
        data = yaml.safe_load(header)
        if not isinstance(data, dict) or data.get("name") != source.parent.name:
            raise ValueError(f"{source}: name must match the directory")
        name = source.parent.name
        target = root / "dist" / "skills" / name / "SKILL.md"
        expected[target] = f"---\n{header}\n---\n\nRead and follow `tmux-a2a-postman skill {name}`.\n"
    if not expected:
        raise ValueError("no skills/*/SKILL.md sources found")
    # Only entries belong in the distribution tree. Never silently ship stale
    # references, helpers or evaluation fixtures from an older layout.
    unexpected = sorted(p for p in (root / "dist" / "skills").rglob("*") if p.is_file() and p not in expected)
    unmanaged = [p for p in unexpected if p.name != "SKILL.md" or p.parent.parent != root / "dist" / "skills"]
    if unmanaged:
        raise ValueError("remove obsolete distribution files: " + ", ".join(str(p) for p in unmanaged))
    if check and unexpected:
        raise ValueError("obsolete Skills: " + ", ".join(str(p) for p in unexpected))
    changed = [p for p, content in expected.items() if not p.is_file() or p.read_text(encoding="utf-8") != content]
    if check and changed:
        raise ValueError("stale Skills; run nix run .#generate-skills: " + ", ".join(str(p) for p in changed))
    for old in unexpected:
        old.unlink()
        old.parent.rmdir()
    for target in changed:
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(expected[target], encoding="utf-8")
    print(f"Skill generation {'check' if check else 'complete'}: {len(expected)} entries")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    try:
        generate(args.root.resolve(), args.check)
    except (OSError, ValueError, yaml.YAMLError) as error:
        print(f"FAIL: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
