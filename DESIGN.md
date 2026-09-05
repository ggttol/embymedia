# DESIGN.md

## Direction
A warm-paper media operations ledger: forest-green signal lines, terracotta annotations, ivory surfaces, and a live service path that makes resource, storage, Emby, task, and Agent state legible at a glance.

Reading this as: an operations application for one technical administrator, industrial/editorial language, signature moment: an interactive live signal spine.

Candidate directions considered:
- Safe: restrained light admin console — clear but interchangeable.
- Bold: media operations ledger — selected; dense facts and live paths read like an operator's annotated runbook.
- Unexpected: film contact-sheet layout — distinctive but weaker for continuous controls.

Ambition: VARIANCE 6 · MOTION 2 · DENSITY 8.

## Color
Source: the Warm Paper reference and operational status conventions.
- background / surface: `#ebe5d6` / `#f8f5eb`
- text / secondary: `#1d241f` / `#4e574f`
- accent / accent-strong: `#164b38` / `#0f382a`
- annotation / danger: `#a6432f` / `#97392f`
- border / focus: `#c9c0ac` / `#a6432f`

## Type
Source: custom rationale; privacy/performance-preserving local font stacks.
- display: condensed local sans stack, 36–54px, 700, tight leading
- headings: local Chinese sans, 18–28px, 650
- body: local Chinese sans, 14–16px
- labels/data: monospace, 11–13px, tabular numerals

## Geometry
- Full-viewport control surface; 240px rail + flexible canvas.
- 8px spacing base; dense data rows separated by rules, not floating cards.
- 6–12px radii only for controls/status modules.
- One elevation level for command drawers; most grouping uses borders and color blocks.

## Components
- Signal spine: four labelled nodes with live service or protocol state.
- Module rail: Dashboard, Resources, Favorites, Files, Tasks, Agent, and Settings.
- Agent console: actual MCP `initialize`, `tools/list`, and safe read-tool calls; discovery-only, success, and failure are distinct states; Hermes setup requests `X-Agent-Token`.
- Configuration matrix: endpoint, filesystem, webhook, destructive-action, and write-only credential settings.
- Destructive controls require explicit confirmation, a server-side enable switch where applicable, and visible upstream errors.

## Composition
Persistent module rail → signal spine/status header → module-specific continuous canvas → contextual action dock. Configuration uses a table/matrix rather than another card grid.

## Motion
Status pulse and 180ms control transitions only. No scroll reveal. Reduced motion removes pulses/transitions while preserving state.

## Responsive behavior
- ≥1100px: fixed rail and full operations canvas.
- 720–1099px: compact top navigation and two-column modules where content permits.
- <720px: wrapped top navigation, single-column modules, vertical signal path, and horizontally scrollable data tables.
