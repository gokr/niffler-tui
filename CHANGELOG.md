# Changelog

All notable changes to niffler-tui are documented here. The format is based
on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this
project aims for [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.2.0] - 2026-09-11

### Added

- **`/restart` command** — quits with a reserved exit code so the installed
  `niffler-tui` wrapper re-runs the binary and picks up rebuilt plugin
  installs; an in-flight turn remains in the session runner and is replayed
  when the client returns. Without the wrapper, `/restart` exits cleanly with
  the documented code.

- **Slash commands in the input history** — up-arrow and ctrl+r only
  recalled prompts: the Enter handler returned after dispatch for any line
  starting with `/`, so `/status`, `/mcp search ...` and friends vanished.
  Submitted commands are now recorded and persisted like prompts, before
  dispatch so a `/session` or `/new` switch still stores the command in the
  session it was typed in. Busy-Enter steering stays out of the history,
  and credentials entered in forms are still never recorded.

- **Read highlighting, grep/files cards, and `/cards`** — read previews now
  syntax-highlight by file extension (chroma, with a style matching the
  theme's markdown code blocks), `grep` renders `grep "pattern" in path`
  with the location dimmed and each match emphasised, and `files` (niffler's
  ls/find) renders its path list. `/cards [on|off]` (also from this cycle)
  shades tool runs with the theme's card background so a run reads as one
  block; it is a per-run display toggle, and themes without a card
  background are unaffected.

- **Per-tool previews and markdown-rendered thinking** — tool-run cards gain
  a `medium` level between `brief` and `full` (now the default; `ctrl+e`
  cycles brief → medium → full → off). At medium each call renders like its
  tool: bash shows `$ command` with the tail of the output, a
  `... (N earlier lines, ctrl+e to expand)` marker, and the exit status plus
  duration; edit reconstructs the call's old/new text as a red/green diff
  with `+A -R` counts; read and write show the head of the file with a
  collapsed-lines hint; unknown tools fall back to name, arguments and
  result head. `full` stops collapsing; `brief` keeps the one-line summary.
  Clicking a card bumps it one level. Tool durations are plumbed through
  live events and replay telemetry. Reasoning blocks now render through a
  thinking-tinted glamour style, so fenced code inside thinking keeps syntax
  highlighting while prose stays dim italic.

- **The TUI's own version and location in `/status`** — `/status` reported
  which harness clone the TUI is talking to but nothing about the TUI
  itself, leaving no way to spot a stale binary. It now lists a `tui:` line
  with the component version and the resolved binary path, plus the plugin
  package's pinned ref for managed installs (read from `plugin_installed`,
  matched on the binary path; version and path alone when plugins is absent
  or the install is a plain `make build`).

- **Color themes (`/theme`)** — the UI chrome (header, transcript roles, tool
  cards, forms, approval gate, context gauge) and the markdown style assistant
  output renders with now come from a selectable theme. Ten palettes ship:
  the compiled-in ANSI default (unchanged rendering, markdown still follows
  `GLAMOUR_STYLE`), light themes for white-background terminals (`light`,
  `sepia`, `solarized-light` — the old default washed out on macOS
  Terminal's white profile), and `solarized-dark`, `gruvbox-dark`, `nord`,
  `dracula`, `tokyo-night`, `catppuccin-mocha`. `/theme [name]` applies and
  persists the choice (like `/locale`, env override `NIF_TUI_THEME`); a bare
  `/theme` opens a picker that previews each palette live as you move the
  selection. Tab completes theme names. Switching mid-conversation repaints
  the transcript (cached block renders and the glamour renderer are rebuilt).

- **Headers field in the /mcp form** — the add/edit form gains a
  `Headers (JSON)` field next to env (blank keeps the stored headers on
  edit, mirroring env), so authenticated http/sse servers can be managed
  entirely from the TUI; `${NAME}` placeholders pass through untouched to
  the bridge's env interpolation. Non-string header values are rejected
  at form time (the wire type is map[string]string); locales updated.

- **MCP registry search** — `/mcp search <keywords>` browses the official
  MCP Registry through the manager's `mcp_search`: installable entries open
  the add form pre-filled (suggested name from the entry id, transport,
  command/args/url); non-installable ones show the requirement reason
  (template variables, required env) instead of a half-filled config.

- **MCP prompt results enter the conversation** — a slash-command result
  carrying `userMessage` (MCP prompt templates, `mcp-<server>-<prompt>`)
  now submits as a user message (steered mid-turn, a normal turn when
  idle) instead of dumping JSON into a meta block; a session switch while
  the render was in flight drops it with a note. Matches the web UI's
  Chat.svelte behavior (docs/WIRE.md userMessage convention). Slash meta
  results now prefer the `text` field over `summary`, like the web UI.

- **MCP form parity with the web UI** — the add/edit form gained effect
  (write/read) and concurrency (parallel/serial) enum cycles plus an idle
  timeout (ms, 0 = 5 minutes) field, all pre-filled on edit and always
  sent; add/edit warnings ("stored but bridge did not start") surface as
  transcript errors; `promptCount` joins the server listing.

- **Live model ids in the connect/edit forms** — the edit form (`e` in
  `/provider`) refines its model prefill with the stored provider's live
  `/models` ids (via the new `provider_models` tool, stored credential);
  the connect form probes with the typed credentials at submit and repairs
  the model id before saving (probe failure still saves). Live ids take
  precedence over the models.dev catalog for prefill and suffix-match
  normalization; the version-tail pick ("5.10" > "5.9", zero-padded
  segments) drives the default.

- **Connect a provider without typing a model id** — the connect/edit
  form fetches the provider's catalog models and prefills the model
  field with an auto-pick (newest tool-call-capable model), so the
  field can simply be Tab-past; the placeholder now says so. Hand-typed
  ids are normalized against the catalog: exact ids are kept as-is and
  missing vendor prefixes are repaired by case-insensitive suffix match
  ("glm-5.3-flash" → "hf:zai-org/GLM-5.3-Flash" for Synthetic).
  Unknown ids still pass through untouched (custom gateways route on
  their own ids); without a catalog the old required-model validation
  applies.

- **Prompt-cache hit ratio in the status detail** — `/status` now shows a
  `cache hits:` line once the provider reports a cached-input breakdown:
  session-wide cached prompt tokens, total prompt tokens, and the hit
  percentage, accumulated across rounds (a round reported on both the
  status and assistant events counts once) and reset on session switch.
  Hidden when the provider gives no breakdown, so it never reads as a
  misleading 0%.

- **Initial Bubble Tea session client** (`e1cd0d4`) — a terminal chat
  client for Niffler: drives `session` calls through `svc.core.call` and
  renders `ev.session.*` token, tool-call, assistant and completion
  events, exactly like the desktop UI. Ships as a Niffler plugin
  (`niffler.json`, `interactive: true`, MIT) built from source by the
  builder, and registers zero tools — running it adds nothing to an
  LLM's tool context. `-session <id>` selects the conversation
  (default `NIF_SESSION` → `console`).

- **Markdown rendering, history, selectors, context gauge** (`70884c6`) —
  assistant replies render as Markdown (Glamour) with per-block caching
  while streaming blocks stay plain and upgrade when output pauses;
  readline-style input history (Up/Down with draft restore, Ctrl+R
  reverse search, JSONL under `XDG_STATE_HOME` capped at 200);
  `/provider` searchable provider selector with env fallback and setup,
  `/model` searchable catalog persisted as a conversation override
  without an inference call, `/connect` masked API-key form from
  models.dev templates or a custom OpenAI-compatible endpoint (secrets
  never enter history or the transcript), `/status` effective
  provider/model provenance plus a context gauge colored at core's
  75%/90% thresholds; multiline editing (Alt+Enter / Ctrl+J), viewport
  key routing, mouse wheel with scroll-up stickiness during streaming.

- **Human approval gate** (`419c76f`) — the client side of the
  multi-client approval protocol (directed requests on
  `svc.approval.tui.request`, acks, stale-dismissal on
  `ev.approval.resolved`): while an approval is pending a modal replaces
  the view (tool name + pretty args, "+N more waiting", active
  auto-approve list); Enter approves, Esc denies, `a` approves + always
  for this session (per-conversation auto-approve memory keyed by
  sessionId + tool).

- **Persisted auto-approve** (`29dfa89`) — auto-approve decisions write a
  store record (kind `approval`, id `<session>:<tool>`) so core's gate
  skips the dialog entirely; other clients no longer see a flashing
  modal for a tool another UI already auto-approved.

- **Collapsible tool-run cards + mouse toggle** (`1fb78d2`) — consecutive
  tool calls in a turn fold into one card (web-UI ToolRun style):
  collapsed by default with a summary line (✓/⚠ glyph, call count, name
  chips, +N past 8); a global key cycles every card between brief/full/
  off (ctrl+e today); with `/mouse on` a left click toggles the card
  under the cursor (hit-test maps terminal Y to the transcript block via
  the viewport scroll offset); `/mouse off` restores native click-drag
  copy/paste. Makefile: `make` / `make test` / `make vet` / `make run` /
  `make clean`.

- **Session switcher + steer/cancel** (`08c9cf8`) — `/new [id]` starts a
  fresh conversation and `/session` lists stored conversations in a
  searchable selector to switch or resume (re-bootstrapping the target's
  model override and runtime); `/provider strip [on|off]` surfaces the
  strip-model-prefix state for gateways that route on the canonical id;
  steering and cancellation on the running turn
  (`svc.session.<id>.steer` / `llm.cancel.<sessionId>`).

- **Provider edit/remove** (`e3fed86`) — `e` in the provider selector
  opens a pre-filled edit form (nickname locked, API key optional so
  stored credentials survive); `d`/`x` removes with a two-stage
  confirmation (arm, then confirm; navigate or esc disarms); actions
  report added/updated/removed/selected distinctly and keep the form
  open on error.

- **Slash-command registry, tab completion, plugin commands** (`7427d2b`)
  — the declarative slash registry core publishes (store checkpoint +
  `ev.catalog.updated`, catalog-snapshot fallback for older cores)
  drives `/help`, tab completion (command names, inline `values`, and
  param candidates fetched from the param's source tool via
  `core.invoke`) and execution of plugin-declared commands as ordinary
  tool calls rendered as meta blocks; built-ins win on name collision
  and local commands never reach the model.

- **Tool-card visibility cycle + thinking-effort rotation** (`3343084`) —
  ctrl+e cycles tool-card display (brief → full → off), consistent with
  mouse hit-testing; ctrl+g rotates the conversation's thinking effort
  (auto → low → medium → high) via the session runner's `thinking`
  argument, persisted per conversation like the model override, applied
  between turns (forwarded as `reasoning_effort` only when set, so
  providers without support never see it), shown as a blue effort chip
  in the header.

- **Thinking display levels** (`256b177`) — ctrl+t cycles reasoning
  visibility (full → brief → off) with a standout header chip; brief
  shows one dim "▸ thinking…" line per block; thinking blocks are
  finalized when a round closes so later rounds' reasoning never stacks
  into the first round's block.

- **en/zh/zh-TW UI locales** (`79492f8`) — i18n.go catalog with {n}
  placeholders, en as the source of truth and test-enforced key parity
  (TestCatalogsComplete), locale resolved from a persisted `/locale`
  choice → `NIF_TUI_LOCALE` → `LANG`/`LC_ALL` (zh_TW/HK → Traditional,
  zh → Simplified) → en; all chrome localized (status line, footers,
  selectors, provider form, approval box, help, /status, history search,
  mouse notices), model-facing strings stay English; `/locale` switches
  at runtime and persists.

- **Bilingual docs** (`aa464b8`, `dad2435`) — Simplified and Traditional
  Chinese READMEs with a language link, plus a Discord invite link.

- **Subscription OAuth logins** (`23c4097`) — the `/provider` selector
  offers ChatGPT Plus/Pro (browser or device code) and Claude Pro/Max
  (browser) sign-ins: `provider_oauth_start` → login panel with the
  authorization URL (opened in the system browser best-effort) and
  device code → `provider_oauth_complete` polling on the
  backend-suggested interval until the credential is stored; a manual
  code/redirect-URL input covers hosts without the fixed localhost
  callback port; esc cancels (`provider_oauth_cancel`). Poll chains are
  sequence-serialized so a manual submit cannot fork parallel polling,
  and stale results from cancelled/superseded chains are dropped;
  OAuth providers get an "OAuth" badge in selectors and the edit form
  tells them to leave the key blank.

- **Application-owned mouse selection** (`0067f5f`) — with tracking on,
  plain left-button drag now selects and copies via app-owned selection
  (selection.go, press/motion/release; cleared on window resize or wheel
  scroll), so wheel scroll, click-to-toggle and copy all work at once;
  `/mouse off` remains the terminal-native fallback.

- **Pi-style input frame + viewport regression probes** (`16066b5`,
  `210f21f`) — single-line input framed by full-width cyan rules with a
  blank spacer so streamed output never sits flush against the input;
  viewport tests drive bubbles v2 with the TUI's exact settings
  (SoftWrap, styled multi-line, trailing CR, wide-rune wrap boundaries,
  bottom pinning) as canaries for ghost-line artifacts.

### Changed

- **Header and bottom-row layout (Pi-inspired)** — the header's runtime line
  now carries the session's cumulative `↑ in ↓ out` token totals and the
  prompt-cache hit rate next to the context gauge (the cache counter is
  restored from the persisted conversation header, and per-conversation stats
  survive switching back and forth within a run). The transient turn state
  moved from the bottom row into the divider above the input, embedded a few
  characters into the line (`── ⠋ working ────`), which frees the bottom row
  for a single line: the conversation workspace (`~`-shortened, tail-kept
  when narrow) and any status note. The command list and key hints are gone
  from that row — `/help` and Tab completion cover them, and `help.keys` now
  lists every binding (alt+enter/ctrl+j, ctrl+r, ctrl+t, ctrl+e, ctrl+g,
  pgup/pgdn, drag-select, esc, ctrl+c). The input placeholder ("message
  (alt+enter: newline)") is removed too.

- **Local commands are declared once** — the built-in registry
  (`builtinSlashCommands`) now carries everything about a command: its
  handler, its aliases (`aliasOf`), its declared subcommands and its params.
  `executeLocalCommand` dispatches through the registry instead of a parallel
  switch, so a local command is one entry that `/help`, Tab completion,
  validation and dispatch all read. Consequences:
  - `/provider strip [on|off]` is discoverable at last — it existed only as a
    string comparison inside the old switch, in no list, help text or README.
  - `/mcp`'s subcommands come from one table, so the enum Tab completes and
    the dispatcher accepts can no longer disagree (they did: `search`/`s` were
    switch-only while the declared enum said `add|edit|on|off|refresh`).
  - `/discover` declares its `<component>|tool=NAME` shape, `/components` its
    filter enum, `/locale` its language enum.
  - `TestEveryBuiltinIsHandled`, the new
    `TestSubcommandsAreDeclaredAndHandled` and the new
    `TestReadmeCommandList` fail CI on the remaining drift directions: a
    registry entry without a handler, a subcommand whose enum and table
    disagree, and a README command list out of step with the registry.

- **Mouse tracking on by default** (`d61aef4`) — the wheel scrolls the
  transcript and clicks toggle cards; copy was Shift+drag (SGR standard)
  until `0067f5f` added plain-drag selection.

- **Thinking rendering compacted** (`9955c6b`, `fb4ad57`) — edge newlines
  are trimmed and interior blank-line runs collapse to a single newline
  so reasoning paragraphs flow densely instead of stacking into walls of
  empty rows.

- **Transcript rendering extracted and cached** (`3df9ad3`, `ea78478`) —
  a dedicated transcript.go owns rendering with a cached joined render;
  history.go owns sent-message history.

- **Backend responses trimmed; bootstrap state loads concurrently**
  (`bbad322`).

- **`/status` shows the output limit** (`045f25a`) — effective max output
  tokens and provenance (`output`/`outputSource` from `llm_resolve`).

- **Test suite** — 100 Go tests across main/slash/i18n/selection/viewport
  files pin rendering, approval flow, slash completion, selection and
  viewport behavior; `make test` runs them.

### Fixed

- **Card background gaps across grouped and wrapped tool-run lines** — tool
  cards are wrapped before their background is painted and every physical row
  is padded to the viewport's safe width. ANSI resets from lipgloss, syntax
  highlighting and shell output are followed by a background repaint, so
  multiline scripts, short continuation rows, narrow terminals and resized
  cards remain solid rectangles. Background-less styles and `/cards off` keep
  the plain path.

- **Restarting resumed the wrong conversation** — the TUI always started on
  `console` (or `NIF_SESSION`) because nothing recorded the session the user
  had switched to. The active conversation is now persisted per harness
  (keyed by the bus URL, next to the theme/locale files) on every `/session`
  switch, `/new` and browser pick, and a plain restart resumes it. Explicit
  `-session` and `NIF_SESSION` still win.

- **Long conversations replayed from their oldest messages** — the store's
  list tool returns the first 1000 documents and has no cursor, so a session
  with more than 1000 messages was replayed from the beginning and its
  recent turns were missing. When the first page is full the TUI now
  binary-searches the padded message-id space with tiny one-item probes and
  fetches the tail window instead, so resumes land on the latest messages.

- **`/components` printed a blank block without a session snapshot** —
  every tool rendered as "unknown", which no filter accepted, so all four
  filters could match nothing, and an empty match set rendered as "". The
  `unknown` filter is now accepted, `undiscovered` treats a missing
  snapshot as not-yet-callable, and an empty result set explains itself
  instead of showing a blank block.

- **Streaming slowed to a crawl as sessions grew** — every token frame
  re-rendered and re-clamped the whole transcript and handed the
  ever-growing string to the viewport, whose internal metrics scan every
  line; per-token cost grew linearly with conversation length (measured
  ~9 ms/op at 100 rounds, ~129 ms/op at 400, allocating tens of MB per
  token). Settled block renderings are now cached and only the streaming
  tail is rebuilt, token repaints are coalesced into ~30fps flushes,
  `View()` no longer splits the frame when nothing is selected, and the
  rendered transcript is capped to a scrollback window (default 3000 lines;
  `NIF_TUI_SCROLLBACK` overrides, `0` = unlimited) with a marker where
  earlier messages are hidden. Per-token cost is now flat at any session
  size (~4 ms including one flush per four tokens); the full transcript
  stays in the session store and every block stays in memory.

- **Startup and `/session` switching showed a blank output area** — the TUI
  resumed the same conversation on the backend but never replayed its
  messages, so the previous turns were invisible and unscrollable (switching
  sessions looked like switching to an empty one). The stored transcript
  (store kind `message`) is now replayed into the output area at connect and
  after every `/session`, `/new` and browser switch: user and assistant
  turns, reasoning blocks, grouped tool cards with their arguments and
  results (arguments are recovered from the assistant message's
  `tool_calls`, the outcomes from the following tool messages), and turn
  errors. Replays are generation-stamped, so a reply that lands after a
  further switch is dropped, and are inserted at the point the load started,
  so a message sent while the store read is in flight is preserved below the
  history.

- **`/help` hid `/components`, `/discover` and `/profile`** — the listing was a
  hand-maintained list of translation keys instead of the command registry, so
  three implemented commands never appeared (they completed via Tab, but
  nothing advertised them) and `/locale`, which was listed, was missing from
  the registry and therefore never completed. `/help` is now generated from the
  registry (declaration order, aliases such as `/sessions` excluded), `/locale`
  is registered with its `en|zh|zh-TW` enum, and `TestHelpListsEveryBuiltin`
  fails CI when a registered built-in is missing from the listing, while
  `TestEveryBuiltinIsHandled` catches the reverse drift (a registry entry the
  dispatcher does not handle). A built-in whose catalog line is missing still
  renders a usage line derived from its declared params.

- **/mcp selector and form never rendered** — the MCP modes switched state
  and handled keys but were missing from the `View()` switch, so `/mcp`
  showed only the header line; all three modes (server browser, registry
  search, add/edit form) now render their list, footer and form.

- **Paragraph gaps between thinking blocks** — reasoning compaction caps
  blank-line runs at one blank line instead of collapsing them to a single
  newline; streamed thinking keeps its paragraph separation while runaway
  empty rows stay bounded (web UI twin: gokr/niffler#12).

- **Anthropic cache reads surfaced** — the Anthropic adapter mapped
  cache-read input tokens into the prompt total but never populated the
  OpenAI-style `prompt_tokens_details.cached_tokens` breakdown, so
  Claude sessions reported no cache-hit data downstream (conversation
  status events, bench, clients). Cache-creation input stays excluded:
  it is a write, not a hit.

- **Long-running tools invisible until completion; drag toggled cards**
  (`0067f5f`) — a tool call only appeared when its result arrived; the
  client now keys session events by `callId` + `phase` and appends a
  pending entry when a tool call starts, so long-running tools show
  immediately, and card activation is delayed until mouse release so a
  drag never toggles a card.

- **Duplicate assistant blocks from cross-subject NATS ordering**
  (`5ee866c`, `03ecc8e`) — token, assistant and toolcall events travel on
  separate subjects without ordering guarantees: a tool-call landing
  between a round's token stream and its final assistant event used to
  spawn a duplicate block (or append a repeated tail). Streaming and
  final events now coalesce into the trailing unfinalized block of their
  kind, and late tokens after a closed round are ignored — the assistant
  event is authoritative.

- **Thinking stacked above the conversation** (`d4184ee`) — the streamed
  thinking block was never finalized when a round closed, so every later
  round's reasoning was appended into the first block, stacking it above
  the conversation.

- **Stale async results after session switches** (`4232d9f`) — bootstrap,
  runtime-refresh and model-action snapshots now carry the session they
  were loaded for and are dropped on mismatch, so switching sessions
  cannot apply another conversation's state.

- **Session switch left the old transcript on screen** (`fc50822`,
  `47b5f88`) — the viewport clears when switching sessions.

- **Rune-safe approval-args truncation** (`f949079`) — byte-slicing
  pretty-printed approval args could split CJK characters mid-rune and
  render invalid UTF-8 in the approval box.

- **Doubled input** (`b201766`) — View() appended the textarea a second
  time.

- **Auto-approve persist off the update loop** (`210a75d`) — the persist
  command returns so it runs outside the update loop.

- **Header merge artifact** (`045f25a`) — a duplicate headerLine
  declaration left by an incomplete header/runtime merge broke the
  build.
