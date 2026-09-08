# DESIGN.md

## Direction
A warm-paper media operations ledger: forest-green signal lines, terracotta annotations, ivory surfaces, and a live service path that makes resource, storage, Emby, task, and Agent state legible at a glance.

Reading this as: an operations application for administrators and media operators, industrial/editorial language, signature moment: an actionable service-status ledger with a task recovery queue.

Candidate directions considered:
- Safe: restrained light admin console — clear but interchangeable.
- Bold: media operations ledger — selected; dense facts and live paths read like an operator's annotated runbook.
- Unexpected: film contact-sheet layout — distinctive but weaker for continuous controls.

Ambition: VARIANCE 4 · MOTION 2 · DENSITY 8.

## Color
Source: the Warm Paper reference and operational status conventions.
- background / surface: `#ebe5d6` / `#f8f5eb`
- text / secondary: `#1d241f` / `#4e574f`
- accent / accent-strong: `#164b38` / `#0f382a`
- annotation / danger: `#a6432f` / `#97392f`
- border / focus: `#c9c0ac` / `#a6432f`

## Type
Source: custom rationale; privacy/performance-preserving local font stacks.
- headings: local Chinese serif, 20–30px; the page owns one primary heading.
- body: local Chinese sans, 14–16px.
- labels: 12–14px; monospace is reserved for paths, identifiers, and tabular data.

## Geometry
- Full-viewport control surface; 232px desktop rail and flexible canvas.
- 8px spacing base; dense data rows separated by rules, not floating cards.
- 4px panel and control radii; pill geometry is reserved for compact status badges.
- One elevation level for command drawers; most grouping uses borders and color blocks.

## Components
- Status: checking, healthy, failed, and untested have explicit text and matching colors; protocol diagrams never imply unobserved success.
- Module rail: Dashboard, Resources, Favorites, Files, Tasks, Agent, Settings, and administrator-only Users.
- Agent console: actual MCP `initialize`, `tools/list`, and safe read-tool calls; discovery-only, success, and failure are distinct states; Hermes setup requests `X-Agent-Token`.
- Configuration matrix: endpoint, filesystem, webhook, destructive-action, and write-only credential settings.
- Authentication entry: a Warm Paper access ledger with a single credential form, explicit private-system context, and no decorative dashboard metrics.
- User administration: a restrained roster table with role, access state, password-reset, and removal actions; destructive changes use explicit confirmation.
- Destructive controls require explicit confirmation, a server-side enable switch where applicable, and visible upstream errors.
- Copy actions acknowledge success only after clipboard completion; unavailable browser APIs expose selectable text for manual copying.
- Dialogs own focus, Escape dismissal, submission feedback, and return focus to their opener.
- Failed reads remain distinct from successful empty results; stale results show their age.
- Automatic episode completion is a locked-scope operations flow: library checkboxes, transfer/preview modes, bounded candidate controls, an explicit destructive completed-pack switch, and a four-stage inspection-to-verification ledger. Replacement state distinguishes staged, retained-old, and completed deletion outcomes.
- Poster maintenance is presented as one inspect–repair–verify task; completed runs remain warnings when any poster is still missing or a refresh request failed.
- Metadata repair pairs one confidence statement with a bounded work limit and explicit auto-apply switch; execution cards separate auto-matched, review, and no-candidate outcomes and expose provider candidates progressively.

## Composition
Persistent navigation → page heading → actionable exceptions and running tasks → compact inventory → secondary trends. Resource workflows preserve search context and name transfer destinations. Agent setup prioritizes connection, configuration, and tokens; tool catalogs and audit detail are progressive disclosure. Settings groups connection, paths, and advanced options.

## Motion
Status pulse and 180ms control transitions only. No scroll reveal. Reduced motion removes pulses/transitions while preserving state.

## Responsive behavior
- ≥1024px: fixed rail and full operations canvas.
- 640–1023px: compact persistent top navigation.
- <640px: five persistent destinations (Overview, Search, Files, Tasks, More); secondary destinations open in an accessible dialog. File rows recompose for touch; save and batch-action bars sit above navigation and safe-area insets.
