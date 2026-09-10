# Documentation rules

Document the current standalone Go and Vue application. Keep the common reader path in the root README; architecture, development, testing, and operations each own their detailed facts. State prerequisites, observable outcomes, failure limits, and recovery risks before advanced implementation detail. Do not present archived runtime designs as available features.

## Bilingual pages

Update English and Chinese counterparts together. Keep matching section order, lists, tables, code blocks, and physical line counts; each prose paragraph occupies one physical line. Use relative Markdown links, including correct localized anchors. End each file with exactly one newline.

Each pair has `name.md`, `name.zh.md`, and `name.i18n.yaml`. The sidecar maps the two basenames to their SHA-1 Git blob IDs: hash UTF-8 bytes prefixed with `blob <byte-length>\0`. Record hashes only after both languages express the same facts. Shared code examples and legal text may remain untranslated. Agent instruction files and explicitly unpaired product/design/evidence documents are not translation pairs.

## Verification and decisions

`make check` runs the documentation checker in [`scripts/check-docs.py`](../scripts/check-docs.py). Describe only checks actually performed. Production commands require separate authorization; do not execute deployment or destructive media operations merely to verify prose.

Non-trivial decisions belong in [Agent Notes](../.agents/notes/README.md). Frozen archive files, sidecars, and existing manifest entries must never be edited, removed, translated, or moved. Archive outbound links are historical and are excluded from current-link checks.
