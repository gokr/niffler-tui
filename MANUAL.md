# niffler-tui Manual

[English](MANUAL.md) · [简体中文](MANUAL.zh.md) · [繁體中文](MANUAL.zh-TW.md) · [Back to README](README.md) · [Niffler harness](https://github.com/gokr/niffler)

Detailed usage and reference. For installation and a first conversation, start
with the [README](README.md). Remote/fleet support is optional and experimental.

## Contents

- [Run](#run) — connection and conversation selection
- [Thinking](#thinking)
- [Command reference](#command-reference)
- [Shell escapes](#shell-escapes)
- [Plugin slash commands](#plugin-slash-commands)
- [Transcript and client state](#transcript-and-client-state)
- [Plugin status](#plugin-status)
- [Build](#build)
- [Fleet startup (experimental)](#fleet-startup-experimental)

## Run

Start Niffler, then run the client from a real terminal:

```bash
./bin/niffler-tui
./bin/niffler-tui -session game
```

Configuration:

- `-session <id>` selects the conversation, defaulting to `NIF_SESSION` and
  then the last session for this harness/workspace; with no saved choice, a
  fresh conversation is created.
- `NIF_NATS_URL` selects the bus. When unset, the client checks
  `$NIF_ROOT/var/nats-url`, then `./var/nats-url`, then
  `nats://127.0.0.1:4222`.
- The UI language resolves from a choice persisted by `/locale`, then
  `NIF_TUI_LOCALE` (`en`/`zh`/`zh-TW`), then `LANG`/`LC_ALL` (zh_TW/zh_HK →
  Traditional, other zh → Simplified), then English.

Keys:

- `Enter`: send the current input; while a turn is running, Enter steers it
  instead — the message is folded into the live turn as a "Steer: …" line
- `Alt+Enter` / `Ctrl+J`: insert a newline in the input (multiline messages)
- `Up` / `Down`: move within the input; at the first/last line they walk
  sent-message history (readline-style), restoring the in-progress draft
- `Ctrl+R`: reverse history search; type to filter, `Ctrl+R` again for older
  matches, `Enter` to accept, `Esc` to cancel
- `Tab` / `Shift+Tab`: complete the slash command or argument under the
  cursor — a dim candidate line appears above the input, `Tab`/`Shift+Tab`
  cycle the highlighted candidate, `Esc` (or any other key) dismisses, and a
  single candidate fills directly
- `PgUp` / `PgDn`: page through the transcript
- `Ctrl+Up` / `Ctrl+Down`: scroll the transcript one line at a time
- `Ctrl+T`: cycle thinking visibility (`full` → `brief` → `off`)
- `Ctrl+E`: cycle tool-card detail (`brief` → `medium` → `full` → `off`)
- `Ctrl+O`: the worker cycle — the worker roster on top (running background
  processes and working subagents, with status and age) and an output pane
  below it filling the rest of the screen: for a background process its raw
  stdout tail, for a subagent the tail of its own transcript (its answers,
  its tool calls and a bounded preview of each outcome), refreshed live while
  the cycle is open. `Ctrl+O`/`Tab`/`n` rotates workers, `r` re-reads, `Esc`
  returns; with nothing running it answers "nothing is running (bg/agent)"
- `Shift+Tab`: rotate the conversation's LLM thinking effort
  (`auto` → `low` → `medium` → `high` → `max`)
- `Esc`: while busy, the first press arms a "Stop?" prompt and the second
  force-cancels the running turn
- Mouse: wheel scrolls the transcript; plain drag selects and copies; left
  click toggles a tool-run card; ctrl+click opens the URL or file path under
  the pointer; alt+drag additionally saves the selection as a snippet
- `Ctrl+X`: rotate the favorite provider/model combinations (`/fav` manages
  them; between turns, like `/model`)
- `Ctrl+C`: quit

Consecutive tool calls in a turn are folded into a single collapsible
"tool-run card". At `medium` (the default) each call renders with a shape
that fits its tool: bash shows `$ command`, the tail of the output with a
`... (N earlier lines, ctrl+e to expand)` marker, and the exit status plus
duration; edit shows the old/new text as a red/green diff with `+A -R` line
counts; read and write show the head of the file with a collapsed-line
hint; other tools fall back to their name, arguments and result head.
`brief` reduces every card to a one-line summary (✓/⚠ glyph, call count,
names) for quiet long tool sequences; `full` expands every call without
collapsing output; `off` hides cards entirely. `Ctrl+E` cycles the levels
and a left click toggles the card under the cursor (brief→medium→full for
that card). Mouse tracking is **on by default**: the wheel scrolls the
transcript, while plain left-button drag selects and copies text using
application-owned selection. Both therefore work at the same time.
Releasing the button is the copy — there is no separate `Cmd+C` step. The
text goes out over OSC 52 and, when the client runs locally (no SSH
session), also through the platform clipboard tool (`pbcopy`, `wl-copy`,
`xclip` or `xsel`), so terminals that ignore OSC 52, such as Apple's
Terminal and GNOME Terminal, still fill the clipboard.
Two modifier gestures ride the same selection. **Ctrl+click** opens what
the pointer is on: a URL in the browser, or a file path — optionally with
a grep-style `:line` suffix, which is dropped — in the platform's default
handler, resolved against the conversation workspace, the launch directory
and the harness root (`NIF_TUI_OPEN` replaces the opener wholesale, e.g.
`NIF_TUI_OPEN='kitty nvim'`). **Alt+drag** additionally saves the selection
as a *snippet* while still copying. Snippets live in the store (kind
`snippet`) with the collecting conversation and a timestamp; `/snippets`
lists them, and the model reads the same documents through the store's own
tools, so both ends of the conversation can recall one later. Both gestures
need a terminal that reports mouse modifiers (SGR mouse encoding — the same
xterm-compatible terminals that make drag selection work).
`/mouse off` remains a terminal-native fallback, but the app then receives
no wheel events, so the transcript scrolls with `PgUp`/`PgDn`/`Ctrl+Up` and
the wheel behaves as ordinary terminal scroll instead.

### Thinking

Model reasoning renders above each assistant reply, placed per round in the
transcript. It is markdown-rendered through a thinking-tinted style: prose
stays dim italic while fenced code inside reasoning keeps syntax
highlighting. `Ctrl+T` cycles how much of it is shown: `full` (everything),
`brief` (one dim `▸ thinking…` line per block) and `off` (hidden entirely).
Reasoning is compacted for display: edge newlines are trimmed and blank-line
runs are capped at one blank line, so paragraph breaks between thinking
blocks stay visible without stacking into walls of empty rows.

`Shift+Tab` rotates the conversation's LLM thinking *effort* —
`auto` (provider default) → `low` → `medium` → `high` → `max`. The selection
is persisted per conversation like the model override, applies between turns,
and is forwarded to the LLM as `reasoning_effort` only when set, so providers
without reasoning-effort support never see it. Levels are not a uniform scale
across providers — DeepSeek, for one, maps `minimal`/`low` to `low`,
`medium`/`xhigh` to `high` and `max` to `max` — so `max` is the only step
above `high` where the provider exposes one. The current selection shows in
the header as `effort:auto|low|medium|high|max`.

### Command reference

Local commands are handled by the TUI and are never sent to the model:

- `/provider` — searchable global provider selector (e: edit, d: remove);
  also offers the environment fallback, provider setup, and subscription
  logins (ChatGPT Plus/Pro via browser or device code, Claude Pro/Max via
  browser). OAuth flows open the authorization URL, poll until Niffler's
  provider component stores the tokens, and accept a pasted code/redirect
  URL when the local callback port is unavailable; esc cancels
- `/model` — searchable model catalog across every configured provider;
  typing filters on provider nickname or model id (no need to press `/` first),
  and picking a model from another provider pins provider AND model for this
  conversation in one persisted override (without an inference call)
- `/connect` — masked provider connection form using models.dev templates or a
  custom OpenAI-compatible endpoint
- `/mcp [add|edit|on|off|refresh|search]` — manage external MCP servers; a bare
  `/mcp` opens the server browser
- `/lsp [add|edit|remove]` — manage language-server registry entries (config,
  not code: register e.g. gopls for .go and the lsp tool gains diagnostics and
  precise navigation for it); a bare `/lsp` opens the server browser
- `/processes` — show and manage background processes (dev servers, watchers
  started via bash's run_in_background or process_start): a status-line
  `bg N` badge while anything runs, enter peeks the output tail without
  touching the model's drain cursor, d kills with a two-stage confirm
- `/tasks` (`/inbox`) — the Fleet inbox: tasks you submitted to remote
  Nifflers with their lifecycle state and, once settled, the peer's bounded
  verdict. A local read, so a job whose instance is down or gone still shows
  what it reported; detail is `fleet_task_result` and needs the peer. A
  settlement arriving while you are connected marks the status line instead of
  taking the viewport. Unread marks are yours alone — they live in the client's
  own state and are never sent to the harness, so nothing acknowledges a model's
  notice. The first connect baselines existing history rather than showing it
  all as new.
- `/status` — detailed effective provider/model/context provenance and usage
- `/info [id]` — conversation statistics computed from the store's message
  documents: message counts per role, per-tool call counts and tool-result
  sizes (total, median, largest, plus the biggest offenders with previews),
  summed token usage, and the discover/invoke breakdown. Defaults to this
  conversation; an id inspects another one (tab-completed), subagents show
  their parent
- `/name [name]` — set this conversation's name (the header title,
  replacing the auto-truncated first-message one); a bare `/name` shows the
  stored name
- `/copy` — put the last assistant reply on the clipboard (the same path
  drag selection uses)
- `/fav [add|rm <n>|<n>]` — favorite provider/model combinations, stored as
  one ordered store document (kind `modelfavorite`); `<n>` applies an entry
  to this conversation, and `Ctrl+X` rotates to the next favorite in order
  (wrapping; between turns, like `/model`)
- `/doctor [deep]` — render a readable Markdown health table; `deep` runs live probes
- `/export [path]` — export the exact current provider-facing request as
  pretty-printed JSON; without a path it renders the JSON in the transcript
  (capped), otherwise it writes the file relative to the conversation workspace
- `/compact` — run the replaceable compactor now (no LLM turn): older history
  is replaced with a verified checkpoint, so a long conversation can be
  compacted before it hits the automatic pressure ladder; a compactor decline
  is reported, never silently degraded to a lossy trim
- `/approvals [ask|auto]` — this conversation's approval gate. `auto` grants
  every `x-harness.approval` tool without asking any client (each grant is
  logged loudly, since a silent grant is what the gate exists to prevent);
  `ask` restores the gate, and no argument reports the current mode. The gate
  modal's `A` key sets the same mode while a request is on screen: two-stage
  (`A`, then `A` again to confirm, like the stop and kill confirmations), and
  it also grants the requests already queued for that conversation. It covers
  program-shaped calls too, which the per-tool `a` deliberately does not — so
  the widest grant needs the extra keypress. `a` keeps its narrower meaning
  (always allow *this* tool for this session).
- `/workspace [path]` — where this conversation works, and the control for the
  one thing the client pins on your behalf: started in a directory, the first
  turn pins it as the conversation's workspace. A bare `/workspace` reports the
  path, its git branch, whether the checkout is a **linked worktree** rather
  than the primary one (with the primary's path), and the granted-root count. A
  path argument **repins** the workspace — allowed only while the conversation
  is pristine, because once a turn has run the workspace is immutable: resumes
  must resolve context and paths exactly as the original turn did, and the
  system prompt is frozen against it. Core refuses past that point and says so;
  start a new conversation instead. Distinct from `/roots`: this is the primary
  root (what relative paths resolve against), `/roots` grants *extra* roots on
  top. The bottom row marks a linked worktree with `⧉` next to the branch.
- `/roots [add|remove <dir>]` — the conversation's working set: a bare
  `/roots` reports the primary workspace (immutable, pinned when the
  conversation is born) and every root granted on top of it; `add` and
  `remove` change the grants, and the bottom row carries the branch plus a
  `+N` count so a grant is visible where the workspace already is. A grant
  widens what tools accept and what the write fence admits, so it stays until
  removed — it is not an OS sandbox. `~` is expanded and a relative path
  resolves against the conversation workspace, since core requires an
  absolute, existing directory. `/roots add ~/git/other-clone`
- `/components [all|direct|discovered|undiscovered]` — what this conversation
  can actually call, per component, with each tool's exposure state
- `/discover <component>|tool=NAME` — expose a component's tools (or one named
  tool) to this conversation, so the model can call them directly
- `/recall <words…>` — search this conversation's whole history (the
  ranked context_recall lane over the store's search index) and show bounded
  one-line hits with their message ids. The transcript's own grep for a human.
- `/snippets [show|ins|del <n>]` — the snippet collection gathered with
  alt+drag (1 = newest). `show` prints the full text, `ins` drops it into
  the input, `del` removes an entry. Snippets are store documents (kind
  `snippet`), so the model can recall them too via the store's tools
- `/agent [name|none]` — the agent definition a NEW conversation is born with:
  the persona it speaks as, the toolset it starts with, and the model, thinking
  and budgets that freeze with it. `none` clears the choice, and a bare
  `/agent` opens a picker showing each definition's building blocks. Client
  state like `/toolset` — a selection applies to conversations created from
  here on and never rewrites one that already exists (the harness refuses
  that: an identity is fixed at birth). The active choice shows in the header
- `/skills [name]` — browse the skills installed on the harness and load one.
  A bare `/skills` opens a menu listing each skill with the discovery root it
  came from (`project`/`bundled`/`home`/`config`) and its description; skills
  this conversation has already loaded are marked `● ` and sorted first, so
  what is already in context is visible rather than remembered. Selecting one
  loads it, and `/skills <name>` loads without opening the menu. The list comes
  from the skills component's `skill_list` and the load goes through the same
  `skill_load` the model uses, so the size cap and the discovery/shadowing rules
  have one implementation — this command replaces the component's own raw-JSON
  listing, which could not show what was loaded or offer a way to load it.
- `/toolset [name|default]` — toolset applied to new conversations;
  `default` clears it, and a bare `/toolset` opens a picker listing the stored
  toolsets with the current choice marked and each one's resolved tool count
  and token cost (`default` is offered as the "standard toolset" entry). The picker's
  `＋ New toolset…` entry opens a form that validates each selector against the
  live catalog before storing: a bare selector names a component, a single tool
  is `component.tool` (so `edit.read`, not `read`), and `-tool` excludes. There
  is no security boundary here — a profile only trims or adds tools for
  conversations created while it is selected.
- `/new [id]` — start a fresh conversation
- `/session` — conversation browser; switch or resume sessions, or start a
  new one. **Subagent sessions are hidden by default** — a conversation that
  delegates work spawns a session per child, and those would otherwise crowd
  out your own conversations. `a` toggles them in (each marked `↳` and naming
  the parent it belongs to); the title shows how many are being held back.
  Typing in the filter box runs a **server-side search** over the harness's
  store (`search`, gokr/niffler#77) — conversations are matched by id and
  title where they live instead of the browser downloading every one of them;
  a store without the tool (an older harness) falls back to filtering the
  loaded list locally
- `/locale [en|zh|zh-TW]` — switch the UI language (persisted)
- `/theme [name]` — switch the UI color theme (persisted); a bare `/theme`
  opens a picker with a live preview, and every theme pairs the chrome
  palette with a matching markdown style. Beyond the compiled-in default
  there are seventeen curated palettes including light themes (`light`,
  `sepia`, `solarized-light`, `github-light`, `rose-pine-dawn`) for
  white-background terminals, plus `solarized-dark`, `gruvbox-dark`, `nord`,
  `dracula`, `tokyo-night`, `catppuccin-mocha`, `monokai`, `one-dark`,
  `github-dark`, `rose-pine`, `kanagawa`, and `everforest`.
  `NIF_TUI_THEME` overrides the startup default
- `/mouse [on|off]` — mouse tracking (default on: wheel, click, and
  plain-drag selection work together; off uses terminal-native selection but
  provides no app wheel)
- `/cards [on|off]` — shade tool runs with the theme's card background so a
  run reads as one block (per-run display toggle)
- `/niffler [NAME|next|previous]` — list or switch instances in-process; each
  visited instance keeps its connection and conversation state. Failed
  verification stays put. `/niffler local` returns to the local instance.
- `/restart` — quit with a reserved exit code so the installed `niffler-tui`
  wrapper re-runs the binary, picking up a rebuilt install after an update;
  run without that wrapper it simply exits. The conversation you were in, and
  this client's identity (its `Niffler N` label), carry over to the successor,
  which resumes it even if the old process never released it
- `/quit` — quit the client, same as ctrl+c; the client identity is released
  normally (no restart handoff), and a turn still running keeps going in its
  session runner, replayed from the store on the next attach
- `/alias <name> <prompt…>` — define a prompt shortcut: typing `/name` (with
  any extra words appended to the stored prompt) sends it as an ordinary turn.
  `/alias add <name> cli call <tool> '<json>'` instead defines a call alias
  that invokes a harness tool directly, with `$1`…`$9` interpolating the
  invocation's arguments. A bare `/alias` or `/alias list` lists the
  definitions and `/alias rm <name>` deletes one; definitions persist in the
  per-user state dir, complete on Tab, and appear in `/help`. Built-in names
  cannot be shadowed
- `/help` — command summary, generated from the registered command table so
  it always lists every built-in (and every plugin command)

Tab completion works for command names and, where a command declares it, for
argument values (inline candidates, or values fetched lazily from the
declared source tool). Unknown commands suggest near-miss names.

### Shell escapes

The two shell markers run a command in your own shell — `$SHELL -c`, in the
conversation's workspace, with this process's environment and `NIF_TUI=1`
set — and print its output in the transcript as a dim block. They are the
human's own command line, so they need no connection, raise no approval gate
(approvals gate the *model's* tool calls, not your keystroke) and are never
sent to the harness; interactive programs (`vim`, `less`, `ssh` into
something) are not supported, because the command has no terminal attached.
Under a remote `NIF_NATS_URL` the command runs on the machine running the
TUI, not on the harness's machine.

- `!<command>` — output stays here, like typing it in another terminal
  (`!git status`, `!make test`).
- `?<command>` — the same command, and its output is then sent to the model
  as a turn: `?git status` runs locally and asks about what came back. If a
  turn is already running the output steers into it instead; if the
  conversation is switched while the command runs, the prompt is dropped
  (the output stays visible, so you can re-ask it).

Output is capped at 8 KB with an explicit `… N bytes truncated` line, and a
timeout bounds a hung command. A bare `!` or `?` is an ordinary prompt, but
otherwise the marker always wins over prose: a line that starts with `!` or
`?` is an escape, so a sentence meant for the model starts without it.

`?` also works on the local commands themselves: `?/status`, `?/doctor deep`,
`?/recall <words>`, `?/info`, `?/components` … run the command exactly as
`/command` does and then send what it printed to the model, capped the same
way. That is the one way to ask about a command's output — the per-command
`ask` argument (`/status ask`, `/recall … ask`, `/doctor … ask`) is gone.
Commands that open a picker or a form (`/session`, `/theme`, `/profile`) have
no output text and refuse the marker, and `!` never runs a slash command.

Selectors use `/` to filter, arrows to move, `Enter` to choose, and `Esc` to
return to chat. The provider form masks its API-key field; credentials
entered in forms are never written to sent-message history or the
transcript. Submitted lines — prompts and slash commands alike — are kept
in the per-session input history, so up-arrow and ctrl+r recall commands
like `/status` or `/mcp search ...` too.

The header shows the conversation id, the `think:`/`tool:`/`effort:` chips,
and the effective provider and model plus a context gauge. Context occupancy
uses model-reported total tokens when available, survives session resume
through Niffler's conversation metadata, and changes colour at the same 75%
warning / 90% trimming thresholds as core. The same line carries the
session's cumulative token totals (`↑ in ↓ out`) and the prompt-cache hit
rate (restored from the conversation header on resume). Provider selection
changes Niffler's global default; model selection is scoped to the current
conversation.

The input zone is bordered by rules; the transient turn state (spinner and
`working`, stopping, the two-stage stop prompt) is embedded a few characters
into the top rule, Pi-style. The single bottom row shows the conversation
workspace and any transient note — the command and key hints live in `/help`
and Tab completion instead.

## Plugin slash commands

Components can extend the TUI declaratively, with no UI code: a component
registers its tools as usual and adds a `slash` section to its registration
(`{name, description, tool, params[]}` with per-parameter completion
sources; see `docs/WIRE.md` in the Niffler repository). Core validates the
spec, checkpoints the merged table to the store, and announces
`ev.catalog.updated` on every change; the TUI reads store-first and follows
the event live.

A registered command runs by parsing the command line against its declared
params (positional values, `name=value`, bare bool flags, defaults) and
calling the target tool; the result renders as a meta block in the
transcript. Built-in commands shadow same-named registrations, and commands
disappear automatically when their component departs.

## Transcript and client state

Assistant replies are rendered as Markdown (headings, bold, italics, lists,
tables, and syntax-highlighted fenced code blocks via Glamour). While tokens
stream, blocks show as plain text and upgrade to styled Markdown when output
pauses, avoiding an expensive full Markdown render on every token.

On startup and after every conversation switch (`/session`, `/new`, or an
entry in the browser), the output area is rebuilt from the session's stored
messages — user and assistant turns, reasoning, grouped tool cards with
their arguments and results, turn errors, and dim `▸` machinery rows for
folded runtime notices (a subagent that settles, a background process that
exits, or the prompt of an autonomous wake turn) — so the previous
conversation is visible and scrollable instead of starting blank. Those
notice rows also arrive live: a parent conversation woken because a child
finished shows the settlement and the reply that followed it, without the
human having to ask. The status line adds `agent N (age)` while subagents
work and a `bg N` badge while background processes run.

The viewport keeps a bounded scrollback window (the last 3000 rendered lines
by default, with a marker where earlier messages are hidden), which is what
keeps streaming cost flat as a session grows: settled blocks are cached and
only the streaming tail is re-rendered, token repaints are coalesced, and
the viewport never scans more than the window. Set `NIF_TUI_SCROLLBACK` to
the number of lines to keep, or `0` for unlimited. The full transcript
always remains in the session store.

All client-local preferences and state live under `~/.niffler/` on Linux and
macOS: `theme`, `locale`, `aliases`, `activity.json` (no instance file: remote
routes come from the harness's fleet registry), keyed
`session-*`/`handoff-*` files, and `history-<session>.jsonl` (capped at 200
entries; delete a history file to clear it). No SQLite database is introduced.

Files migrate on first use from the previous platform config directory's
`niffler/` and `$XDG_STATE_HOME/niffler-tui/` (default
`~/.local/state/niffler-tui/`). Existing destination files win; migrated files
are private (0600), and successfully migrated sources are removed. Close older
TUI versions before upgrading: they still write the old locations. XDG variables
are consulted only to locate legacy files, not to select the new destination.
Harness store data and skill directories are unchanged.

The Markdown style follows the `GLAMOUR_STYLE` environment variable (default
`dark`; see Glamour's styles, e.g. `light`, `ascii`, `dracula`,
`tokyo-night`).

The alternate screen keeps the viewport and input stable while tokens stream.
When the transcript is scrolled up, new tokens do not force it back to the
bottom.

## Plugin status

`niffler.json` marks the component as `"interactive": true`: it should be
built by plugin installation but started manually from the user's terminal:

```bash
./var/bin/cli install gokr/niffler-tui
./var/bin/tui -session game
```

Interactive components are not supervised or restarted by Niffler. Quit any
running TUI before updating or removing the package.

### Lima migration

`lima` profiles use native `limactl shell --workdir=/home/ubuntu NAME` and the guest CLI stdio relay; root is `/home/ubuntu/niffler`. Optional SSH host routing, direct SSH and Incus remain supported. Old Multipass receipts and registry `multipass` fields explicitly refuse: provision a new Lima guest and import its verified receipt; never rename old routes or copy identity. Fleet pins Ubuntu digests, disables mounts/credential forwarding/automatic port forwarding, and enables guest persistent user service. Real Lima smoke remains pending separate authorization.

## Build

This checkout is expected next to Niffler so the local `go.mod` replacement
finds the Go SDK:

```bash
make            # builds bin/niffler-tui
make test       # runs the TUI test suite
make run        # build + run
```

or directly:

```bash
go build -o bin/niffler-tui ./tui
```

The Niffler builder does not use this `go.mod`; it creates an isolated module
with a replacement pointing at its own `sdk/go` directory.


## Fleet startup (experimental)

Remote instances come from the harness's own fleet registry
(`fleet_instances`, store kind `fleetinstance`), which holds the route and
identity pin the harness verified. There is no client-side instance file: the
client always has its local harness, so nothing has to be remembered before the
first connection.

A peer that is already registered needs only its name:

```bash
./bin/niffler-tui --instance-probe worker
./bin/niffler-tui --instance worker
```

A peer that is not registered yet is named with its route for that launch:

```bash
./bin/niffler-tui --instance worker --ssh ubuntu@worker
./bin/niffler-tui --instance worker --incus worker [--host operator@hypervisor]
./bin/niffler-tui --instance worker --lima worker
./bin/niffler-tui --instance worker --bus nats://127.0.0.1:4222
```

Exactly one route per launch, and the identity it reaches is learned from the
live catalog — the launch flags never assert an identity. Guests default to
`/home/ubuntu/niffler`; `--instance-root` overrides it for SSH targets. To make
a peer appear without flags, register it on the harness (`fleet_install` or
`fleet_instance_import`) and it shows up in `/niffler` and the ring.

The ring follows the registry: Alt+N / Alt+Shift+N walk the reachable
instances and skip a peer that fails to connect, reporting it and trying the
next candidate; when every candidate has failed the client stays where it was
and says so. SSH uses normal configuration/known_hosts with agent forwarding
disabled; Lima uses native `limactl shell --workdir=/home/ubuntu` plus the
worker's native `<root>/var/bin/cli relay <port>`, without keys, host mounts or
a public listener. Rebuild/update the worker's harness first: its CLI must
support `relay` (POSIX, loopback-only); a missing or outdated CLI refuses with
no fallback. No Python runtime or transferred helper is needed. No remote
service is started or stopped. Missing targets and identity mismatches refuse;
there is no local fallback. Use the matching harness wrapper to ensure remote
options bypass its local autostart. Plain launches remain unchanged.

Incus receipts (`transport: "incus"`) connect through `incus exec` and the
native relay as the guest's ordinary `ubuntu` user, never through guest SSH.
The Incus root is `/home/ubuntu/niffler`; commands explicitly select the local
daemon and `default` project, ignoring mutable client remote/project defaults. Incus and Lima receipts may include
`host: "gokr@wowbagger.krampe.se"`: management commands then execute through SSH
on that host, with every command argument shell-quoted. Omit `host` for local
guests. No management API or NATS listener needs public exposure. SSH keys and
known_hosts belong to the client; no agent forwarding or copied guest keys.
Bootstrap/update are approved harness fleet operations, not TUI startup effects.

### Switching between instances

One client holds a live connection per instance you have visited, and switching
is **in-process**: no re-exec, and work on the instance you leave keeps running.

| Input | Effect |
|---|---|
| `Alt+N` / `Alt+Shift+N` | Next / previous instance (wraps) |
| `/niffler` | List instances with their state |
| `/niffler NAME` | Switch to NAME (registration is required first) |
| `/niffler next` / `previous` | Same rotation from a command |

Each instance keeps its own conversation, workspace, draft, scroll position and
connection; nothing is shared between them, and a session ID on one instance is
never assumed to mean anything on another. The bottom row names the active
instance and shows every visited one with its state:

```
● local   ○ worker ✓2   Alt+N next · Alt+Shift+N prev · /niffler
```

`●` marks the instance on screen, `✓N` counts conversations that finished while
you were elsewhere, `!approval` marks one waiting on a human, and `…`/`⚠` mean
still connecting / disconnected. `/niffler local` returns to this machine — the
answering core is checked against the local root rather than trusted, so a
return can never attach to a stranger's bus. Approvals on an instance you are
not viewing are shown but never answered for you.

### Completion notices

A turn that finishes on another instance raises that instance's unread badge;
opening the conversation clears it. Reads are client-local and separate from
the agent's own notice acknowledgement, so opening a finished conversation
does not consume anything the model still needs and does not start a turn.
Several completions in one conversation collapse to a single unread marker.

Live pushes cover the steady state; on connect and after a reconnect the client
pages the harness's durable completion snapshot, so a finish that happened while
the client was away is not lost. A killed turn or a failed transcript write may
leave no receipt at all: absence means unknown, never success.

### Remote specifics

Requires a harness SDK with `sdk.NewWithURL` and
`Component.OnReconnected`; an older SDK fails at compile time rather than
degrading.

Remote workspace defaults to the receipt root, not the laptop's cwd. Saved last
session/restart state uses the stable instance ID, not the forwarding port, so a
new tunnel port does not orphan history. Local path completion and transcript
file opening are disabled remotely; attachments still refer to explicitly
selected local files. Each forwarding connection re-resolves the remote endpoint
and verifies the pinned catalog identity before forwarding SDK traffic,
including reconnects; a mismatch closes that lane with no fallback. That uses a
separate probe connection, not authentication against a malicious endpoint
swap. `/restart` now keeps you on the instance you were on. Background approvals
are not implemented yet.

When a target cannot be reached, the client says which part failed instead of
reporting the closed socket: a bare `EOF` is what a dead forwarding lane looks
like from here, never the cause. A refusal names the target, keeps the transport
error, and adds the relay's own diagnosis, for example

```
Instance switch refused: no harness answering at worker: the guest's advertised
bus (127.0.0.1:4222) answered no catalog probe: EOF; relay to the guest failed:
cli relay: Connection refused — is its harness running?
```

An identity mismatch is reported as itself, since it is more precise than the
lane it closed. A target harness is never started for you: run it there, ideally
as a resident user service (see Niffler's manual, "Persistent server lifecycle").


## Architecture

A Bubble Tea terminal client for Niffler. It is a bus client like the desktop
UI: it drives `session` calls through `svc.core.call` and renders
`ev.session.*` token, tool-call, assistant, and completion events.

The component registers with zero tools. Running it does not add anything to
an LLM's tool context.

