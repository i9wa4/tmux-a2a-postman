# Documentation reorganization evidence — 2026-11-02

This note records source/example and presentation checks for #982. It does not
publish a release, activate a configuration, or establish third-party indexing.
The source includes #981's server snapshot and #884's optional finite launcher;
their PRs must land before this documentation is merged as the main entry point.

## 1. Ownership decisions

| Surface                    | Decision                                                                                                                                                                                   |
| -------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| README                     | Keep value, organization, architecture, request/reply diagrams, source installation and direct navigation.                                                                                 |
| User operations            | Move detailed first-use, capture/notification caveats and workflow orientation to `help/postman-usage/references/quickstart.md`; link existing exact contracts instead of copying schemas. |
| Configuration              | Keep the canonical reference inside the installed config-auditor Skill; add anatomy, a self-contained example, exact substitutions and progressive repository examples.                    |
| CONTRIBUTING               | Own check commands, fixture matrices, dependency procedure and evidence limits; retain links to dated validation and cloud debugging.                                                      |
| AGENTS / CLAUDE            | Keep essential development checks, isolation, portability and release boundaries in AGENTS; CLAUDE remains its pointer. Remove repeated long recipes.                                      |
| RELEASING                  | Retain as the single owner of release authorization, tags and publishing. No procedural rewrite needed.                                                                                    |
| Skills                     | Keep mandatory operating instructions/references installable; remove the obsolete README setup anchor without adding a checkout requirement.                                               |
| Historical design/research | Retain dated evidence and retired proposals; identify research as experiments, not setup or a shipped platform.                                                                            |

The pre-edit map was recorded in the task's existing private tracker. No second
public specification, documentation platform or runtime instruction surface was
introduced.

## 2. Automated source checks

`TestDocumentedConfigurations` loads each complete example with the real bounded
parser and recipient-policy reader, including selected shipped Skill metadata,
reviewer/interface designations, inert presets and direct hierarchy routes.
The installed reference's complete example is extracted and parsed separately.
`TestDocumentedPracticalConfigRendersRecipientSpecificContext` checks actual
common/recipient/catalog/contact/exact-reply rendering and substitutions.

`TestDocumentedMinimalFirstExchange` runs the existing real-tmux fixture with
the minimal file unchanged, in both plain and bracketed editors. Real input
drives receiver-owned POP, complete archive reading and exact reply; the
controller does not supply recipient actions. No model/client credentials are
required. Local focused run: config/CLI checks passed; terminal exchange passed
in 12.80 s. Required hooks, Nix checks/build and platform CI results are
recorded on the PR.

Local link auditing covers changed documents, target files and heading anchors
after Markdown formatting. Installed Skill metadata and deterministic trigger
checks remain part of the normal checks; required resources are not moved into
repository-only guides.

## 3. Presentation checks

All four README diagrams were rendered with Mermaid 12.0.0 in Chromium, using
ordinary loopback-served local assets. Light/dark themes at 1280 px and 390 px
were inspected; diagrams fit the viewport without horizontal page overflow.
The sequence uses supported Mermaid configuration to keep its three columns
readable on a narrow screen. Each diagram has a prose caption/navigation path.
Local rendering is separate from GitHub's independently deployed renderer;
the actual public branch rendering check is reported on the PR.

## 4. DeepWiki decision

Keep the badge absent.
[CognitionAI/deepwiki](https://github.com/CognitionAI/deepwiki) documents
badge-based refresh eligibility, but open
[#11](https://github.com/CognitionAI/deepwiki/issues/11) reports image/Camo
failures and [#17](https://github.com/CognitionAI/deepwiki/issues/17) reports
stale badge-bearing indexes. Those are upstream reports, not an independently
reproduced diagnosis of this repository. Retrieval limits do not establish a 404
or a repaired service. A local image/alternate badge has no verified equivalent
detection contract.

Badge rendering, an existing index, and an automatic index advance are separate
claims. None is inferred from this documentation change. Future restoration
requires official supported markup, actual GitHub rendering and useful refresh
evidence. No indexing job, paid service, guessed API or automated submission is
added. Authored docs and tested examples remain authoritative.
