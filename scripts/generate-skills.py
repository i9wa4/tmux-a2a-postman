#!/usr/bin/env python3
"""Generate thin installable Skills from canonical HelpDoc frontmatter."""

import argparse
from pathlib import Path
import sys

import yaml


def generate(root, check=False):
    expected = {}
    for source in sorted((root / "help").glob("*/SKILL.md")):
        text = source.read_text(encoding="utf-8")
        if not text.startswith("---\n") or "\n---\n" not in text[4:]:
            raise ValueError(f"{source}: expected YAML frontmatter")
        header, _ = text[4:].split("\n---\n", 1)
        data = yaml.safe_load(header)
        if not isinstance(data, dict) or data.get("name") != source.parent.name:
            raise ValueError(f"{source}: name must match the directory")
        metadata = data.get("metadata", {})
        body = metadata.get("postman-skill-body") if isinstance(metadata, dict) else None
        if not isinstance(body, str) or not body.strip() or len(body.strip().splitlines()) != 1:
            raise ValueError(f"{source}: metadata.postman-skill-body must be one nonempty line")
        target = root / "skills" / source.parent.name / "SKILL.md"
        expected[target] = f"---\n{header}\n---\n\n{body.strip()}\n"
    if not expected:
        raise ValueError("no help/*/SKILL.md sources found")
    # Only entries belong in the distribution tree. Never silently ship stale
    # references, helpers or evaluation fixtures from an older layout.
    unexpected = sorted(p for p in (root / "skills").rglob("*") if p.is_file() and p not in expected)
    if unexpected:
        raise ValueError("remove obsolete distribution files: " + ", ".join(str(p) for p in unexpected))
    changed = [p for p, content in expected.items() if not p.is_file() or p.read_text(encoding="utf-8") != content]
    if check and changed:
        raise ValueError("stale Skills; run nix run .#generate-skills: " + ", ".join(str(p) for p in changed))
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
