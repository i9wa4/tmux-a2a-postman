"""Protect the single source and the deliberately thin distribution boundary."""

from pathlib import Path
import runpy
import tempfile
import unittest


GENERATE = runpy.run_path(str(Path(__file__).resolve().parents[1] / "generate-skills.py"))["generate"]
HEADER = """---
name: postman-example
license: MIT
description: |
  USE FOR: Example work.
  DO NOT USE FOR: Other work.
metadata:
  postman-skill-body: >-
    Read and follow `tmux-a2a-postman help postman-example`.
---
"""


class GeneratedSkillTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.source = self.root / "help/postman-example/SKILL.md"
        self.source.parent.mkdir(parents=True)
        self.source.write_text(HEADER + "\nDetailed procedure.\n")
        self.target = self.root / "skills/postman-example/SKILL.md"

    def test_lossless_header_single_line_and_idempotence(self):
        GENERATE(self.root)
        self.assertEqual(self.target.read_text(), HEADER + "\nRead and follow `tmux-a2a-postman help postman-example`.\n")
        before = self.target.stat().st_mtime_ns
        GENERATE(self.root)
        GENERATE(self.root, check=True)
        self.assertEqual(self.target.stat().st_mtime_ns, before)

    def test_check_is_readonly_and_detects_missing_and_edited_outputs(self):
        with self.assertRaisesRegex(ValueError, "stale Skills"):
            GENERATE(self.root, check=True)
        self.assertFalse(self.target.exists())
        GENERATE(self.root)
        self.target.write_text("manually edited\n")
        with self.assertRaisesRegex(ValueError, "stale Skills"):
            GENERATE(self.root, check=True)
        self.assertEqual(self.target.read_text(), "manually edited\n")

    def test_metadata_and_entry_body_changes_follow_the_source(self):
        GENERATE(self.root)
        self.source.write_text(self.source.read_text().replace("Example work.", "Updated work.").replace("Read and follow", "First read"))
        with self.assertRaisesRegex(ValueError, "stale Skills"):
            GENERATE(self.root, check=True)
        GENERATE(self.root)
        self.assertIn("Updated work.", self.target.read_text())
        self.assertTrue(self.target.read_text().endswith("First read `tmux-a2a-postman help postman-example`.\n"))

    def test_references_helpers_and_evaluations_are_never_distributed(self):
        for filename in ["references/guide.md", "scripts/helper.py", "evals/eval.yaml"]:
            path = self.source.parent / filename
            path.parent.mkdir(parents=True)
            path.write_text("private to the full HelpDoc package\n")
        GENERATE(self.root)
        self.assertEqual([p.relative_to(self.root / "skills").as_posix() for p in (self.root / "skills").rglob("*") if p.is_file()], ["postman-example/SKILL.md"])
        stale = self.target.parent / "references/old.md"
        stale.parent.mkdir()
        stale.write_text("stale installed procedure\n")
        with self.assertRaisesRegex(ValueError, "obsolete distribution files"):
            GENERATE(self.root, check=True)

    def test_invalid_source_and_empty_inventory_fail(self):
        for source in ["no frontmatter", HEADER.replace("name: postman-example", "name: wrong"),
                       HEADER.replace("  postman-skill-body: >-", "  unused: >-"),
                       HEADER.replace("    Read and follow", "    Another line.\n    Read and follow").replace(">-", "|-"),
                       HEADER.replace("    Read and follow `tmux-a2a-postman help postman-example`.", "    ")]:
            with self.subTest(source=source):
                self.source.write_text(source)
                with self.assertRaises(ValueError):
                    GENERATE(self.root)
                self.assertFalse(self.target.exists())
        self.source.unlink()
        with self.assertRaisesRegex(ValueError, "no help"):
            GENERATE(self.root)


if __name__ == "__main__":
    unittest.main()
