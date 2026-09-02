# Changelog

All notable changes to niffler-tui are documented here. The format is based
on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this
project aims for [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

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
