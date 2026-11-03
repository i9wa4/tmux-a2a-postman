"""Negative controls prove unsafe or vacuous suites never start an executor."""

import os
from pathlib import Path
import runpy
import shutil
import subprocess
import sys
import tempfile
import unittest

import yaml

ROOT = Path(__file__).resolve().parents[2]
VALIDATOR = ROOT / "scripts/validation/validate-skill-triggers.py"
PREFLIGHT = runpy.run_path(str(VALIDATOR))["preflight"]


class TriggerGateTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        shutil.copytree(ROOT / "skills", self.root / "skills")
        # Nix-store sources are read-only. Fault injection mutates only these
        # private copies, including when tests run as an unprivileged builder.
        for path in self.root.rglob("*"):
            path.chmod(0o700 if path.is_dir() else 0o600)
        self.spec = self.root / "skills/postman-config-auditor/evals/trigger/eval.yaml"
        self.task = self.spec.parent / "tasks/01-positive.yaml"
        self.calls = self.root / "executor-called"
        self.spy = self.root / "spy"
        self.spy.write_text(f"#!{sys.executable}\nfrom pathlib import Path\nPath({str(self.calls)!r}).touch()\n")
        self.spy.chmod(0o700)

    def rejected_before_executor(self):
        result = subprocess.run([sys.executable, str(VALIDATOR), "--root", str(self.root), "--waza", str(self.spy)],
                                capture_output=True, text=True, check=False)
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("FAIL:", result.stderr)
        self.assertFalse(self.calls.exists(), "preflight ran an executor before validating every suite")

    def edit(self, path, change):
        value = yaml.safe_load(path.read_text())
        change(value)
        path.write_text(yaml.safe_dump(value, sort_keys=False))

    def test_current_inventory_and_manual_suites_stay_separate(self):
        names = {path.parent.name for path in (self.root / "skills").glob("*/SKILL.md")}
        for path in (self.root / "skills").glob("*/evals/eval.yaml"):
            path.write_text("executor: forbidden-model\nthis: is deliberately not a trigger suite\n")
        self.assertEqual({name for name, _, _ in PREFLIGHT(self.root)}, names)

    def test_zero_discovery(self):
        for path in (self.root / "skills").glob("*/evals/trigger/eval.yaml"):
            path.unlink()
        self.rejected_before_executor()

    def test_one_missing_suite(self):
        self.spec.unlink()
        self.rejected_before_executor()

    def test_new_skill_requires_coverage(self):
        directory = self.root / "skills/postman-uncovered-fixture"
        directory.mkdir()
        (directory / "SKILL.md").write_text("---\nname: postman-uncovered-fixture\n---\nUncovered\n")
        self.rejected_before_executor()

    def test_missing_default_or_model_executor(self):
        original = self.spec.read_text()
        for executor in [None, "copilot", "claude", ""]:
            with self.subTest(executor=executor):
                self.spec.write_text(original)
                self.edit(self.spec, lambda value: value["config"].pop("executor") if executor is None else value["config"].update(executor=executor))
                self.rejected_before_executor()

    def test_wrong_skill_source_or_name(self):
        self.edit(self.spec, lambda value: value.update(skill="postman-usage"))
        self.rejected_before_executor()

    def test_zero_tasks(self):
        for path in (self.spec.parent / "tasks").glob("*.yaml"):
            path.unlink()
        self.rejected_before_executor()

    def test_expectation_is_required_and_boolean(self):
        original = self.task.read_text()
        for expected in [None, "true", 1, "yes"]:
            with self.subTest(expected=expected):
                self.task.write_text(original)
                self.edit(self.task, lambda value: value.update(expected={} if expected is None else {"should_trigger": expected}))
                self.rejected_before_executor()

    def test_non_trigger_graders_never_execute(self):
        original = self.task.read_text()
        for kind in ["model", "llm", "code", "file", "behavior"]:
            with self.subTest(kind=kind):
                self.task.write_text(original)
                self.edit(self.task, lambda value: value["graders"][0].update(type=kind))
                self.rejected_before_executor()

    def test_suite_grader_cannot_hide_model_work(self):
        self.edit(self.spec, lambda value: value.update(graders=[{"type": "model"}]))
        self.rejected_before_executor()

    def test_wrong_grader_reference(self):
        self.edit(self.task, lambda value: value["graders"][0]["config"].update(skill_path="skills/postman-usage/SKILL.md"))
        self.rejected_before_executor()

    def test_duplicate_yaml_keys(self):
        self.task.write_text(self.task.read_text() + "id: duplicate-key\n")
        self.rejected_before_executor()

    def test_model_resources_are_not_a_trigger_task(self):
        self.edit(self.task, lambda value: value.update(resources=["remote:model-resource"]))
        self.rejected_before_executor()

    def test_escape_or_aliased_paths_are_rejected(self):
        target = self.root / "outside.yaml"
        self.task.rename(target)
        self.task.symlink_to(target)
        self.rejected_before_executor()

    def test_real_grader_rejects_an_incorrect_authored_expectation(self):
        waza = os.environ.get("WAZA_BIN") or shutil.which("waza")
        self.assertIsNotNone(waza, "run in nix develop or set WAZA_BIN to the pinned evaluator")
        self.edit(self.task, lambda value: (value["expected"].update(should_trigger=False),
                                          value["graders"][0]["config"].update(mode="negative")))
        PREFLIGHT(self.root)  # Valid metadata; the actual grader must fail it.
        result = subprocess.run([sys.executable, str(VALIDATOR), "--root", str(self.root), "--waza", waza],
                                capture_output=True, text=True, check=False, timeout=60)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("trigger evaluation failed", result.stderr)


if __name__ == "__main__":
    unittest.main()
