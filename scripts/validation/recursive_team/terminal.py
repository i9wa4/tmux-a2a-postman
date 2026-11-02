"""Use Postman's common terminal editor with the recursive fixture recipient."""

import json
import os
import pathlib
import sys

nonce, mode = sys.argv[1:]
role = os.environ["POSTMAN_SMOKE_ROLE"]
evidence = pathlib.Path(os.environ["POSTMAN_SMOKE_EVIDENCE"])
(evidence / (role + ".launch.json")).write_text(
    json.dumps({"role": role, "initial_prompt": False, "nonce": nonce})
)
source = pathlib.Path(__file__).resolve().parents[1] / "terminal_agent.py"
# Reuse the same terminal protocol without forking its editor.
# Its sibling recipient action is the team actor below; no controller invokes it.
sys.argv = [str(pathlib.Path(__file__)), nonce, mode]
exec(  # noqa: S102 - load only the pinned test editor, with the local recipient adapter
    compile(source.read_text(), str(source), "exec"),
    {"__name__": "__main__", "__file__": __file__},
)
