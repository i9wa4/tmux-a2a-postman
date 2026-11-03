"""Private fixture assembly preserves policy and rejects invalid test trees."""

from pathlib import Path
import unittest

from recursive_team.configuration import compile_config, policy_text, role_sections

TEMPLATE = (Path(__file__).resolve().parents[2] / "testdata/config/recursive.md").read_text()


class RecursiveConfigurationTests(unittest.TestCase):
    def test_finite_tree_preserves_the_complete_authored_policy(self):
        sessions = [{"name": "t0"}] + [
            {"name": "t" + str(i), "parent": "t" + str(i - 1)}
            for i in range(1, 100)
        ]
        result = compile_config(TEMPLATE, {"sessions": sessions})
        self.assertIn('session_name: "t99"', result)
        self.assertIn('parent_session_name: "t98"', result)
        self.assertEqual(result.split("\n---\n", 1)[1], TEMPLATE.split("\n---\n", 1)[1])
        sections = role_sections(result)
        self.assertIn("recursive-common-EOF", sections["common_template"])
        self.assertIn("recursive-messenger-EOF", sections["messenger"])

    def test_policy_comparison_allows_heading_depth_but_not_lost_text_or_code(self):
        source = "### Contract\nComplete instructions through EOF.\n````markdown\n# literal heading\n```\n## still literal\n````\n"
        rendered = source.replace("### Contract", "#### Contract")
        self.assertEqual(policy_text(source), policy_text(rendered))
        for broken in [rendered.replace("through EOF.", ""),
                       rendered.replace("# literal heading", "literal heading"),
                       rendered.replace("## still literal", "still literal")]:
            self.assertNotEqual(policy_text(source), policy_text(broken))

    def test_invalid_or_disconnected_trees_fail(self):
        for sessions in [
            [], [{"name": "a"}, {"name": "b"}],
            [{"name": "a", "parent": "missing"}],
            [{"name": "a"}, {"name": "a"}],
            [{"name": "a", "parent": "a"}],
            [{"name": "root"}, {"name": "a", "parent": "b"}, {"name": "b", "parent": "a"}],
            [{"name": "a:b"}], [{"name": "a", "authority": "all"}],
        ]:
            with self.subTest(sessions=sessions), self.assertRaises(ValueError):
                compile_config(TEMPLATE, {"sessions": sessions})

    def test_ambiguous_policy_or_existing_tree_is_not_replaced(self):
        for text in [
            TEMPLATE.replace("`common_template`", "`missing_common`"),
            TEMPLATE + "\n## `common_template`\nduplicate\n",
            TEMPLATE.replace("skill_path:", "workspace_tree: []\nskill_path:"),
        ]:
            with self.subTest(text=text[:50]), self.assertRaises(ValueError):
                compile_config(text, {"sessions": [{"name": "root"}]})


if __name__ == "__main__":
    unittest.main()
