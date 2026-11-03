# Reviews — independent evidence and accountable convergence — recovery

## 1. Failure-Mode Handling

- Invalid review packets enter `QUARANTINED`, a non-substantive pre-round
  rejection that consumes no failed-rework slot. Do not trust packet-supplied
  finding IDs or treat the packet as approval, a finding wave, or rework.
  Guardian records the packet alias, missing/rejected fields, and required
  resubmission: fresh alias, required packet fields, projected evidence, and
  the cheapest verifier needed to make it reviewable.
- Missing evidence keeps the affected ID `OPEN` unless the ledger records a
  named accepted-risk owner. Local fixtures, repository read-back, external
  read-back, publication receipts, and production evidence are distinct
  evidence tiers.
- Authority gaps are `BLOCKED` or `ESCALATE`: name the exact decision owner,
  the IDs affected, and the smallest manual decision required. Reviewers do not
  approve their own authority gaps.
- Rework must address one frozen material batch. New material discoveries are
  classified and added to the ledger, but do not silently broaden the current
  closure target.
- Cap exhaustion stops the loop with `BLOCKED`: include attempted rounds, open
  IDs, remaining evidence gaps, and the human user through the interface node
  as the only owner who can authorize another round. Name the separate
  authority and owner needed to accept or defer each risk. This
  three stage caps are independent as specified below; neither structural
  correction nor quarantine spends a substantive rework slot.
- Every criterion change creates a fresh successor ID and gives the retained
  predecessor a `changed criterion` round disposition. Link both entries with
  old and new closure conditions, reason, and owner. A same-ID criterion update
  is invalid; never describe a tightened condition as frozen in an earlier
  batch.
