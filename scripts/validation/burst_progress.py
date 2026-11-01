"""Read-only progress evidence for the isolated eight-request approval fixture."""


def approval_progress(evidence, state, completed):
    # A tool may still be finishing bounded notification/capture work after
    # claiming or publishing its decision. Final fixture proofs come later.
    claims = len(list(state.glob("*/agents/approver/read/m-*.pop.json")))
    decisions = len(list(state.glob("*/agents/approver/sent/m-*.md")))
    proofs = len(list(evidence.glob("approver.claim.*.json")))
    return claims, decisions, proofs, completed
