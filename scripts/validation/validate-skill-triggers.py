#!/usr/bin/env python3
"""Preflight every deterministic trigger suite before starting any evaluator."""

import argparse
import os
from pathlib import Path
import re
import subprocess
import sys

import yaml


class InvalidSuite(ValueError):
    pass


class UniqueLoader(yaml.SafeLoader):
    def construct_mapping(self, node, deep=False):
        result = {}
        for key_node, value_node in node.value:
            key = self.construct_object(key_node, deep=deep)
            if not isinstance(key, str) or key in result:
                raise InvalidSuite("mapping keys must be unique strings")
            result[key] = self.construct_object(value_node, deep=deep)
        return result


# Match unambiguous YAML/JSON booleans; YAML 1.1 yes/no/on/off are strings here.
UniqueLoader.yaml_implicit_resolvers = {
    key: [(tag, regex) for tag, regex in values if tag != "tag:yaml.org,2002:bool"]
    for key, values in yaml.SafeLoader.yaml_implicit_resolvers.items()
}
UniqueLoader.add_implicit_resolver("tag:yaml.org,2002:bool", re.compile(r"^(?:true|false)$"), list("tf"))


def metadata(value, path):
    for key in ["name", "description", "version"]:
        if key in value and (not isinstance(value[key], str) or not value[key].strip()):
            raise InvalidSuite(f"{path}: {key} must be a nonempty string")
    if "tags" in value and (not isinstance(value["tags"], list) or any(
            not isinstance(tag, str) or not tag.strip() for tag in value["tags"])):
        raise InvalidSuite(f"{path}: tags must be a string list")


def load(path):
    try:
        text = path.read_text(encoding="utf-8")
        if len(text.encode()) > 256 * 1024:
            raise InvalidSuite("file exceeds 256 KiB")
        if any(isinstance(token, (yaml.tokens.AliasToken, yaml.tokens.AnchorToken)) for token in yaml.scan(text)):
            raise InvalidSuite("YAML anchors/aliases are not supported")
        value = yaml.load(text, Loader=UniqueLoader)
        if not isinstance(value, dict):
            raise InvalidSuite("expected a mapping")
        return value
    except (OSError, UnicodeError, yaml.YAMLError, InvalidSuite) as error:
        raise InvalidSuite(f"{path}: {error}") from error


def mapping(value, allowed, required, label):
    if not isinstance(value, dict) or set(value) - set(allowed) or set(required) - set(value):
        raise InvalidSuite(f"{label}: unsupported/missing fields (allowed: {', '.join(allowed)})")
    return value


def local_file(root, path):
    if not path.is_file() or path.resolve() != path.absolute() or not path.resolve().is_relative_to(root):
        raise InvalidSuite(f"{path}: expected an unambiguous repository-local regular file")


def preflight(root):
    skills = sorted((root / "skills").glob("*/SKILL.md"))
    suites = sorted((root / "skills").glob("*/evals/trigger/eval.yaml"))
    if not skills or not suites:
        raise InvalidSuite("zero shipped skills or deterministic trigger suites discovered")
    names = {path.parent.name for path in skills}
    suite_names = {path.parents[2].name for path in suites}
    if names != suite_names:
        raise InvalidSuite(f"suite coverage mismatch: missing={sorted(names - suite_names)}, orphan={sorted(suite_names - names)}")
    for skill in skills:
        local_file(root, skill)
    inventory = []
    for spec in suites:
        local_file(root, spec)
        name = spec.parents[2].name
        suite = mapping(load(spec), ["name", "description", "skill", "version", "config", "tasks", "metrics", "graders"],
                        ["name", "skill", "config", "tasks"], str(spec))
        metadata(suite, spec)
        if suite["skill"] != name or not isinstance(suite["name"], str) or not suite["name"].strip():
            raise InvalidSuite(f"{spec}: suite must target its corresponding shipped skill")
        # Do not let Waza defaults, suite graders, resources or hooks introduce
        # an SDK/model-backed lane. Tasks own one verified trigger grader each.
        if suite.get("graders", []) != [] or suite.get("metrics", []) != []:
            raise InvalidSuite(f"{spec}: suite graders/metrics are outside the supported trigger contract")
        config = mapping(suite["config"], ["executor", "model", "trials_per_task", "timeout_seconds", "parallel"],
                         ["executor", "model", "trials_per_task", "timeout_seconds", "parallel"], str(spec))
        if config != {"executor": "mock", "model": "offline-mock", "trials_per_task": 1,
                      "timeout_seconds": 30, "parallel": False}:
            raise InvalidSuite(f"{spec}: require the explicit bounded offline mock configuration")
        if type(config["parallel"]) is not bool or type(config["trials_per_task"]) is not int or type(config["timeout_seconds"]) is not int:
            raise InvalidSuite(f"{spec}: invalid configuration scalar types")
        if suite["tasks"] != ["tasks/*.yaml"]:
            raise InvalidSuite(f"{spec}: task discovery must be exactly tasks/*.yaml")
        tasks = sorted((spec.parent / "tasks").glob("*.yaml"))
        if not tasks:
            raise InvalidSuite(f"{spec}: zero task files")
        ids, expectations = set(), set()
        for task_path in tasks:
            local_file(root, task_path)
            task = mapping(load(task_path), ["id", "name", "description", "tags", "inputs", "expected", "graders"],
                           ["id", "name", "inputs", "expected", "graders"], str(task_path))
            metadata(task, task_path)
            if not isinstance(task["id"], str) or not task["id"].strip() or task["id"] in ids:
                raise InvalidSuite(f"{task_path}: missing/duplicate task ID")
            ids.add(task["id"])
            if not isinstance(task["name"], str) or not task["name"].strip():
                raise InvalidSuite(f"{task_path}: nonempty task name required")
            inputs = mapping(task["inputs"], ["prompt"], ["prompt"], str(task_path))
            if not isinstance(inputs["prompt"], str) or not inputs["prompt"].strip():
                raise InvalidSuite(f"{task_path}: nonempty prompt required")
            expected = mapping(task["expected"], ["should_trigger"], ["should_trigger"], str(task_path))["should_trigger"]
            if type(expected) is not bool:
                raise InvalidSuite(f"{task_path}: expected.should_trigger must be boolean")
            expectations.add(expected)
            if not isinstance(task["graders"], list) or len(task["graders"]) != 1:
                raise InvalidSuite(f"{task_path}: exactly one trigger grader required")
            grader = mapping(task["graders"][0], ["type", "name", "config"], ["type", "name", "config"], str(task_path))
            if grader["type"] != "trigger" or not isinstance(grader["name"], str) or not grader["name"].strip():
                raise InvalidSuite(f"{task_path}: only the verified trigger grader is allowed")
            params = mapping(grader["config"], ["skill_path", "mode"], ["skill_path", "mode"], str(task_path))
            if params != {"skill_path": f"skills/{name}/SKILL.md", "mode": "positive" if expected else "negative"}:
                raise InvalidSuite(f"{task_path}: grader source/mode must match the skill and boolean expectation")
        if expectations != {True, False}:
            raise InvalidSuite(f"{spec}: both positive and negative cases are required")
        inventory.append((name, spec, len(tasks)))
    return inventory


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument("--waza", default=os.environ.get("WAZA_BIN", "waza"))
    parser.add_argument("--preflight-only", action="store_true")
    args = parser.parse_args()
    root = args.root.resolve()
    try:
        suites = preflight(root)
        print(f"Preflight: {len(suites)} skills, {sum(n for _, _, n in suites)} deterministic tasks", flush=True)
        if args.preflight_only:
            return 0
        for name, spec, count in suites:
            print(f"Trigger check: {name} ({count} cases)", flush=True)
            result = subprocess.run([args.waza, "--no-update-check", "run", str(spec)],
                                    cwd=root, check=False, timeout=60)
            if result.returncode:
                raise InvalidSuite(f"{name}: trigger evaluation failed (exit {result.returncode})")
        print("Deterministic skill triggers: PASS", flush=True)
        return 0
    except (InvalidSuite, OSError, subprocess.TimeoutExpired) as error:
        print(f"FAIL: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
