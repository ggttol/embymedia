# DESIGN.md

## Direction
A cinema projection-booth operations console: warm black metal, projector amber, status green, ivory type, and a live media-signal spine that makes CloudDrive → STRM → Emby → client state legible at a glance.

Reading this as: an operations application for one technical administrator, industrial/utilitarian language, signature moment: an interactive live signal spine.

Candidate directions considered:
- Safe: restrained dark admin console — clear but interchangeable.
- Bold: projection booth control desk — selected; it belongs to a media system and supports dense operations.
- Unexpected: film contact-sheet editorial layout — distinctive but weaker for live controls.

Ambition: VARIANCE 6 · MOTION 3 · DENSITY 8.

## Color
Source: product-world rationale (projection booth, film leader, equipment status).
- background / foreground: `#11110f` / `#f2eddf`
- surface / foreground: `#1b1b17` / `#f2eddf`
- primary / foreground: `#e6a537` / `#17120a`
- accent / foreground: `#6fba78` / `#0d160f`
- muted / foreground: `#25251f` / `#a9a495`
- border / focus / destructive: `#3a392f` / `#f4bd5b` / `#df6b57`

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
- Signal spine: four labelled nodes with real health state.
- Module rail: Dashboard, Libraries, Resources/Series, Metadata, Tasks, Users, Configuration, and Audit; AI Conversation stays a persistent handoff action.
- Status ticker: write mode, scheduler, credentials, last refresh.
- Configuration matrix: endpoint settings and write-only credential status/actions.
- Action controls launch explicit DSH prompts; unsupported actions show exact blocker text.

## Composition
Persistent module rail → signal spine/status header → module-specific continuous canvas → contextual action dock. Configuration uses a table/matrix rather than another card grid.

## Motion
Status pulse and 180ms panel transitions only. No scroll reveal. Reduced motion removes pulses/transitions while preserving state.

## Responsive behavior
- ≥1100px: rail + full dashboard.
- 720–1099px: compact icon/text rail, two-column modules.
- <720px: 4×2 top module matrix, single-column data rows, sticky action dock; signal spine becomes horizontal scroll-snap.
