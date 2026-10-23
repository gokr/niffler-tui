# niffler-tui Manual

[English](MANUAL.md) · [简体中文](MANUAL.zh.md) · [繁體中文](MANUAL.zh-TW.md) · [返回 README](README.zh.md) · [Niffler harness](https://github.com/gokr/niffler)

> 🤖 AI 自动翻译；如有歧义，以 [English](MANUAL.md) 为准。章节标题保留英文，以保持跨语言链接一致。

详细使用说明与参考。安装和首次对话请从 [README](README.zh.md) 开始。远程/fleet 支持可选，且属于实验功能。

## Contents

- [Run](#run) — 连接与会话选择
- [Thinking](#thinking)
- [Command reference](#command-reference)
- [Shell escapes](#shell-escapes)
- [Plugin slash commands](#plugin-slash-commands)
- [Transcript and client state](#transcript-and-client-state)
- [Plugin status](#plugin-status)
- [Build](#build)
- [Fleet startup (experimental)](#fleet-startup-experimental)

## Run

先启动 Niffler，再从真实终端运行客户端：

```bash
./bin/niffler-tui
./bin/niffler-tui -session game
```

配置：

- `-session <id>` 选择会话；默认依次使用 `NIF_SESSION`、该框架/工作区上次保存的会话。如无已保存选择，则创建新会话。
- `NIF_NATS_URL` 选择总线。未设置时，依次检查 `$NIF_ROOT/var/nats-url`、`./var/nats-url`，最后使用 `nats://127.0.0.1:4222`。
- 界面语言依次取自 `/locale` 保存的选择、`NIF_TUI_LOCALE`（`en`/`zh`/`zh-TW`）、`LANG`/`LC_ALL`（zh_TW/zh_HK 为繁体，其他 zh 为简体），最后为英语。

按键：

- `Enter`：发送输入；轮次正在执行时改为引导当前轮次，消息以 “Steer: …” 行并入正在执行的轮次。
- `Alt+Enter` / `Ctrl+J`：插入换行，支持多行输入。
- `Up` / `Down`：在输入内移动；到达首行/末行时按 readline 风格浏览已发送历史，并恢复尚未发送的草稿。
- `Ctrl+R`：反向搜索历史；输入过滤，再按 `Ctrl+R` 查找更早匹配，`Enter` 接受，`Esc` 取消。
- `Tab` / `Shift+Tab`：补全光标处的斜杠命令或参数。候选行显示在输入上方；按键循环选择，`Esc` 或其他按键关闭，仅有一个候选时直接填入。
- `PgUp` / `PgDn`：翻页滚动记录。
- `Ctrl+Up` / `Ctrl+Down`：逐行滚动记录。
- `Ctrl+T`：切换推理可见程度（`full` → `brief` → `off`）。
- `Ctrl+E`：切换工具卡片详细程度（`brief` → `medium` → `full` → `off`）。
- `Ctrl+O`：打开工作单元轮询视图。上方列出后台进程和子代理，显示状态与运行时间；下方显示后台进程 stdout 尾部，或子代理记录尾部（回复、工具调用及受限长度的结果预览），打开时实时刷新。`Ctrl+O`/`Tab`/`n` 切换工作单元，`r` 重新读取，`Esc` 返回。没有运行任务时提示 “nothing is running (bg/agent)”。
- `Shift+Tab`：轮换当前会话的 LLM 推理力度（`auto` → `low` → `medium` → `high` → `max`）。
- `Esc`：忙碌时首次按下显示 “Stop?”，再次按下强制取消当前轮次。
- 鼠标：滚轮滚动记录；普通拖动选择并复制；左键切换工具调用卡片；ctrl+单击打开指针下的 URL 或路径；alt+拖动还会将选区保存为片段。
- `Ctrl+X`：轮换收藏的提供商/模型组合（由 `/fav` 管理；与 `/model` 一样，在轮次之间生效）。
- `Ctrl+C`：退出。

同一轮次连续的工具调用会合并为一张可折叠的工具卡片。默认 `medium` 模式按工具类型展示：bash 显示 `$ command`、输出尾部、`... (N earlier lines, ctrl+e to expand)` 提示、退出状态及耗时；edit 用红/绿差异显示修改前后文本和 `+A -R` 行数；read/write 显示文件开头和折叠提示；其他工具显示名称、参数和结果开头。

`brief` 将卡片压缩为一行摘要（✓/⚠、调用次数和名称）；`full` 展开所有调用且不折叠输出；`off` 隐藏卡片。`Ctrl+E` 切换级别，左键单击则仅切换该卡片（brief→medium→full）。

**默认开启鼠标跟踪**：滚轮滚动和应用内左键拖动选择可以同时使用。松开鼠标即复制，无需再按 `Cmd+C`。文本通过 OSC 52 发送；本地运行（非 SSH 会话）时还通过平台剪贴板工具（`pbcopy`、`wl-copy`、`xclip` 或 `xsel`）复制，因此不支持 OSC 52 的 Apple Terminal、GNOME Terminal 也能写入剪贴板。

**Ctrl+单击**在浏览器中打开 URL，或通过系统默认程序打开文件。grep 风格的 `:line` 后缀会被移除；路径相对于会话工作区、启动目录及框架根目录解析。`NIF_TUI_OPEN` 可替换整个打开命令，例如 `NIF_TUI_OPEN='kitty nvim'`。

**Alt+拖动**除复制外，还保存片段。片段存入 store（kind `snippet`），带所属会话和时间戳；`/snippets` 可列出，模型也能通过 store 工具读取。上述手势要求终端能报告鼠标修饰键（SGR 鼠标编码，即支持拖动选择的 xterm 兼容终端）。

`/mouse off` 可恢复终端原生选择，但应用不再接收滚轮事件；此时使用 `PgUp`/`PgDn`/`Ctrl+Up` 滚动记录，滚轮仅滚动终端本身。

### Thinking

模型推理显示在每次助手回复上方，按轮次排列。推理正文采用暗色斜体 Markdown，代码块保留语法高亮。`Ctrl+T` 在 `full`（全部）、`brief`（每块一行暗色 `▸ thinking…`）和 `off`（隐藏）间切换。展示时会删除首尾换行，并将连续空行压缩为一个，避免大块空白。

`Shift+Tab` 调整推理**力度**：`auto`（提供商默认）→ `low` → `medium` → `high` → `max`。选择与模型覆盖一样按会话保存，在轮次之间生效；只有明确设置后才以 `reasoning_effort` 发送，不支持此功能的提供商不会收到该字段。

不同提供商的级别并非统一尺度。例如 DeepSeek 将 `minimal`/`low` 映射为 `low`，`medium`/`xhigh` 映射为 `high`，`max` 映射为 `max`；提供商支持时，`max` 是唯一高于 `high` 的级别。标题栏显示 `effort:auto|low|medium|high|max`。

### Command reference

本地命令由 TUI 处理，不发送给模型：

- `/provider` — 可搜索的全局提供商选择器（e 编辑、d 删除），提供环境回退、连接设置和订阅登录（ChatGPT Plus/Pro 浏览器或设备码；Claude Pro/Max 浏览器）。OAuth 打开授权 URL 并轮询，直到 provider 组件保存令牌；本地回调端口不可用时可粘贴代码/重定向 URL，Esc 取消。
- `/model` — 搜索所有已配置提供商的模型。直接输入即可按昵称或模型 ID 过滤，无需先按 `/`；跨提供商选择模型时，将提供商和模型一起固定到当前会话，不触发推理调用。
- `/connect` — 使用 models.dev 模板或自定义 OpenAI 兼容端点填写遮蔽凭据的连接表单。
- `/mcp [add|edit|on|off|refresh|search]` — 管理外部 MCP 服务；不带参数打开服务浏览器。
- `/lsp [add|edit|remove]` — 管理语言服务器注册项。通过配置而非代码注册（如 gopls 对应 .go），使 lsp 工具获得诊断和精确导航；不带参数打开浏览器。
- `/config [domain]` —— 浏览并编辑组件声明的设置：只输入 `/config` 列出所有配置域
  （所有者、设置项数量、变更何时生效），`/config repomap.gates` 打开该域的设置项。
  选择一项、输入新值，回车即通过 harness 的 `config_patch` 提交，并附带你当时看到的
  修订号，因此并发修改会显示为冲突而不是丢失更新。密钥显示为 `set`/`unset`，客户端
  从不持有其值；只读项会被标记；所有者未运行的配置域会列出原因，而不是在提交时才失败。
  表单由各声明的 schema 生成，新所有者无需修改客户端。
- `/processes` — 管理由 bash 的 run_in_background 或 process_start 启动的开发服务、监视器等后台进程。运行时状态栏显示 `bg N`；Enter 预览输出尾部但不改变模型的读取游标，d 经两步确认后终止进程。
- `/tasks`（`/inbox`）— 舰队收件箱：你提交给远程 Niffler 的任务，及其生命周期状态与（结算后的）有界结论。`/tasks <jobId>` 打开单个任务的报告；该读取需要对方应答，因此实例离线时会显示「详情不可用」，而结论仍留在收件箱中。这是一次本地读取，因此即使实例已下线或消失，任务仍会显示它曾报告的内容；详情由 `fleet_task_result` 获取，需要对方在线。连接期间到达的结算只在状态栏提示，不会抢占视图。未读标记只属于你，保存在客户端自身状态中，绝不发送给 harness，因此不会确认模型的任何通知。首次连接会把已有历史作为基线，而不是全部显示为新消息。
- `/status` — 显示实际提供商、模型、上下文来源和用量。
- `/info [id]` — 从 store 消息计算会话统计：各角色消息数、各工具调用数、结果大小（总和、中位数、最大值及带预览的大结果）、累计 token 和 discover/invoke 比例。默认当前会话；可用补全选择其他 ID，子代理显示父会话。
- `/name [name]` — 设置会话名称（标题栏标题，替换由首条消息截取的名称）；不带参数显示已保存名称。
- `/copy` — 将最后一次助手回复复制到剪贴板，使用与拖动选择相同的复制机制。
- `/fav [add|rm <n>|<n>]` — 管理收藏的提供商/模型组合，保存为有序 store 文档（kind `modelfavorite`）。`<n>` 应用条目，`Ctrl+X` 按顺序循环，在轮次之间生效。
- `/doctor [deep]` — 显示 Markdown 健康检查表；`deep` 执行实时探测。
- `/export [path]` — 将当前实际提供商请求导出为格式化 JSON。不带路径时显示在记录中（有长度限制）；带路径时相对于会话工作区写文件。
- `/compact` — 立即运行可替换的压缩组件，不执行 LLM 会话轮次。旧历史被验证过的检查点替换；长会话可在自动压力处理前主动压缩。组件拒绝时会明确报告，不静默退化为有损裁剪。
- `/approvals [ask|auto]` — 控制当前会话审批。`auto` 无需询问客户端即批准所有带 `x-harness.approval` 的工具，并明确记录每次批准；`ask` 恢复询问，不带参数显示模式。审批框中连按两次 `A` 设置同一模式，并批准当前会话已排队请求；它也覆盖程序形式调用，而单工具 `a` 不覆盖，因此最宽授权需要额外确认。`a` 仍仅表示该会话始终允许此工具。
- `/workspace [path]` — 当前会话的主工作区。首次轮次固定启动目录；不带参数显示路径、git 分支、是否为链接工作树（及主树路径）、获准根目录数。路径参数仅在尚无轮次的全新会话中允许重新固定；开始运行后工作区不可变，以保证恢复时路径与冻结提示一致。之后 core 会拒绝并要求新建会话。它与 `/roots` 不同：工作区是相对路径的主根，roots 额外授权其他根。底部用 `⧉` 标识链接工作树。
- `/roots [add|remove <dir>]` — 管理会话工作目录集合。不带参数列出出生时固定的主工作区和额外根；add/remove 修改授权，底部显示分支和 `+N`。授权会扩大工具与写入围栏允许范围，直到明确移除，但不是 OS 沙箱。支持 `~`，相对路径按工作区解析；core 要求绝对且存在的目录。例如 `/roots add ~/git/other-clone`。
- `/components [all|direct|discovered|undiscovered]` — 按组件显示当前会话可调用工具及暴露状态。
- `/discover <component>|tool=NAME` — 将某组件的工具或一个具名工具暴露给当前会话，使模型可直接调用。
- `/recall <words…>` — 使用 store 搜索索引上的 context_recall 排名检索当前会话完整历史，显示带消息 ID 的受限长度单行结果，相当于面向用户的记录 grep。
- `/snippets [show|ins|del <n>]` — 管理 alt+拖动采集的片段（1 为最新）。show 显示全文，ins 插入输入框，del 删除；片段是 kind `snippet` 的 store 文档，模型也能读取。
- `/agent [name|none]` — 选择**新会话**出生时使用的代理定义，包括 persona、初始工具集、模型、推理和预算。none 清除选择；不带参数打开定义选择器，显示各组成部分。与 `/toolset` 一样，选择只影响以后创建的会话，不重写已有会话（框架固定出生身份）。标题栏显示当前选择。
- `/skills [name]` — 浏览框架已安装技能并加载。菜单显示技能描述与来源根（`project`/`bundled`/`home`/`config`）；当前会话已加载项标记 `● ` 并置顶。选择后加载，也可直接指定名称。列表来自 skill_list，加载使用与模型相同的 skill_load，因此大小限制、发现和遮蔽规则只有一套实现；替代无法标识已加载状态或交互加载的原始 JSON 列表。
- `/toolset [name|default]` — 选择新会话初始工具集。default 清除选择；不带参数打开选择器，标记当前项并显示解析后的工具数与 token 成本，default 显示为标准工具集。`＋ New toolset…` 表单在存储前对照实时目录验证选择器：单独名称表示组件，单工具写为 `component.tool`（如 `edit.read` 而非 `read`），`-tool` 排除。工具集不是安全边界，只调整选择生效期间创建的新会话工具。
- `/new [id]` — 创建新会话。
- `/session` — 浏览、切换、恢复或创建会话。**默认隐藏子代理会话**，避免每次委派产生的子会话挤满列表。a 切换显示，子会话用 `↳` 标记并显示父会话；标题显示隐藏数量。过滤框使用框架 store 的服务端 search（gokr/niffler#77），按 ID/标题检索，不下载所有会话；旧框架无该工具时回退为本地过滤已加载列表。
- `/locale [en|zh|zh-TW]` — 切换并保存界面语言。
- `/theme [name]` — 切换并保存颜色主题；不带参数打开实时预览选择器，界面与 Markdown 样式配套。除内置默认外，有十七种精选配色：浅色包括 `light`、`sepia`、`solarized-light`、`github-light`、`rose-pine-dawn`，另有 `solarized-dark`、`gruvbox-dark`、`nord`、`dracula`、`tokyo-night`、`catppuccin-mocha`、`monokai`、`one-dark`、`github-dark`、`rose-pine`、`kanagawa`、`everforest`。`NIF_TUI_THEME` 可覆盖启动默认值。
- `/mouse [on|off]` — 鼠标跟踪默认开启，滚轮、单击和普通拖动可同时用；关闭改用终端原生选择，但没有应用滚轮事件。
- `/cards [on|off]` — 使用主题卡片背景为工具调用组着色，使其呈现为整体（当前进程内显示开关）。
- `/niffler [NAME|next|previous]` — 在进程内列出或切换实例。已访问实例保留连接和会话状态；验证失败保持原实例。`/niffler local` 返回本地。
- `/restart` — 以保留退出码结束，让安装的启动器重新运行更新后的二进制；没有启动器时仅退出。会话及客户端身份（`Niffler N`）传给后继进程，即使旧进程未释放身份也能恢复。
- `/quit` — 与 Ctrl+C 一样退出，正常释放客户端身份，不产生重启交接。仍在运行的轮次由 session runner 继续，下次连接从 store 重放。
- `/alias <name> <prompt…>` — 定义提示快捷方式。输入 `/name` 时将存储提示及额外参数作为普通轮次发送。`/alias add <name> cli call <tool> '<json>'` 定义直接工具调用别名，`$1`…`$9` 插入调用参数。`/alias` 或 `/alias list` 列出，`/alias rm <name>` 删除；定义保存在用户状态目录，支持 Tab，出现在 `/help`，不能遮蔽内置名称。
- `/help` — 从注册命令表生成摘要，始终列出所有内置及插件命令。

Tab 补全命令名，也补全命令声明支持的参数值（内置候选或延迟从声明的源工具读取）。未知命令会提示相近名称。

### Shell escapes

两个 shell 标记在当前会话工作区使用本进程环境和 `NIF_TUI=1`，执行用户自己的 `$SHELL -c` 命令，并以暗色块显示输出。它们是用户自己的命令行，不需要总线连接或审批（审批针对模型调用而非用户按键），也不发送给框架。因为没有附加终端，不支持 `vim`、`less`、交互式 ssh 等。远程 `NIF_NATS_URL` 下仍在运行 TUI 的机器执行，而非框架机器。

- `!<command>` — 输出仅留在本地记录，类似另开终端执行 `!git status`、`!make test`。
- `?<command>` — 执行后将输出作为轮次发送给模型，例如 `?git status`。有运行轮次时作为引导输入；执行中切换会话则不再发送提示，输出仍保留，可重新提问。

输出上限 8 KB，截断时明确显示 `… N bytes truncated`，超时限制挂起命令。单独 `!` 或 `?` 是普通提示，否则标记优先：以它们开头的行总按 shell 转义处理，发给模型的句子不要这样开头。

`?` 也适用于本地命令：`?/status`、`?/doctor deep`、`?/recall <words>`、`?/info`、`?/components` 等。先执行命令，再按相同限制将显示内容发给模型。这是询问命令输出的方式；旧的逐命令 ask 参数（如 `/status ask`）已移除。打开选择器/表单的命令（`/session`、`/theme`、`/profile`）没有文本输出，会拒绝该标记；`!` 不执行斜杠命令。

选择器用 `/` 过滤、箭头移动、Enter 选择、Esc 返回。提供商表单遮蔽 API key；表单凭据不写入输入历史或会话记录。提交的普通提示和斜杠命令都会存入输入历史，因此上箭头和 Ctrl+R 也能找回 `/status`、`/mcp search ...` 等。

标题栏显示会话 ID、`think:`/`tool:`/`effort:`、实际提供商和模型及上下文仪表。上下文优先使用模型报告的总 token，通过会话元数据跨恢复保存，在与 core 相同的 75% 警告 / 90% 裁剪阈值变色。还显示累计 token（`↑ in ↓ out`）和从标题元数据恢复的提示缓存命中率。提供商选择修改全局默认，模型选择仅作用于当前会话。

输入区域由横线界定，旋转图标、`working`、停止中和两步停止提示嵌入上边线。底部单行显示工作区和短暂提示；命令/按键提示放在 `/help` 和 Tab 补全中。

## Plugin slash commands

组件无需编写 UI 代码即可声明式扩展 TUI：正常注册工具，并在注册中加入 `slash`（`{name, description, tool, params[]}`），含参数补全来源，见 Niffler 仓库 `docs/WIRE.md`。Core 验证、将合并表保存到 store，并在变化时发布 `ev.catalog.updated`；TUI 先读 store，再跟随实时事件。

注册命令按声明解析位置参数、`name=value`、裸布尔标记和默认值，调用目标工具，结果显示为记录中的元信息块。内置命令遮蔽同名注册，组件离开时命令自动消失。

## Transcript and client state

助手回复通过 Glamour 渲染 Markdown，支持标题、粗体、斜体、列表、表格和高亮代码块。流式输入时先显示纯文本，暂停时升级为 Markdown，避免每个 token 都完整渲染。

启动或切换会话（`/session`、`/new`、浏览器选择）时，从 store 消息重建输出：用户/助手轮次、推理、带参数结果的工具卡片、轮次错误和暗色 `▸` 系统通知行（子代理结算、后台进程退出、自主唤醒提示）。历史可见且可滚动。通知也实时到达；子任务完成唤醒父会话时会显示结算和随后的回复，无需用户询问。子代理运行时显示 `agent N (age)`，后台进程显示 `bg N`。

默认视口保留最后 3000 行渲染结果，较早内容隐藏处显示标记。已完成块被缓存，仅重新渲染流式尾部；token 重绘合并，扫描不超过窗口，使长会话成本保持平稳。`NIF_TUI_SCROLLBACK` 设置行数，0 不限；完整记录始终保留在 store。

Linux 和 macOS 的客户端偏好及状态统一位于 `~/.niffler/`：`theme`、`locale`、`aliases`、`activity.json`（不再有实例文件：远端路由来自 harness 的 fleet 注册表）、按键值区分的 `session-*`/`handoff-*`，以及上限 200 条的 `history-<session>.jsonl`。删除历史文件即可清空。不引入 SQLite。

首次使用从旧平台配置目录的 `niffler/` 和 `$XDG_STATE_HOME/niffler-tui/`（默认 `~/.local/state/niffler-tui/`）迁移。已有目标优先；迁移文件权限为 0600，成功后删除源文件。升级前关闭旧客户端，它们仍写旧位置。XDG 仅用于寻找旧文件，不决定新目标。框架 store 数据和技能目录不变。

Markdown 样式遵循 `GLAMOUR_STYLE`（默认 `dark`，也可为 `light`、`ascii`、`dracula`、`tokyo-night` 等）。备用屏幕保持视口和输入稳定；向上滚动后，新 token 不会强制跳到底部。

## Plugin status

`niffler.json` 将组件标记为 `"interactive": true`：插件安装只构建，用户从终端手动启动：

```bash
./var/bin/cli install gokr/niffler-tui
./var/bin/tui -session game
```

交互组件不受 Niffler 监督或自动重启。更新/删除插件前退出运行中的 TUI。

### Lima migration

`lima` 配置使用 `limactl shell --workdir=/home/ubuntu NAME` 和 guest CLI 标准输入输出 relay，根为 `/home/ubuntu/niffler`。保留可选 SSH host 路由、直接 SSH 和 Incus。旧 Multipass 收据和 `multipass` 注册字段明确拒绝；应创建 Lima guest 并导入其验证收据，不要重命名旧路由或复制身份。Fleet 固定 Ubuntu 摘要，禁用挂载、凭据转发、自动端口转发，并启用 guest 持久用户服务。真实 Lima 冒烟测试仍需单独授权。

## Build

开发 checkout 预期与 Niffler 相邻，以便 go.mod 的本地 replacement 找到 Go SDK：

```bash
make            # 构建 bin/niffler-tui
make test       # 执行 TUI 测试
make run        # 构建并运行
```

或直接执行：

```bash
go build -o bin/niffler-tui ./tui
```

Niffler builder 不使用此 go.mod，而是创建隔离模块，replacement 指向其自己的 `sdk/go`。

## Fleet startup (experimental)

远端实例来自 harness 自身的 fleet 注册表（`fleet_instances`，store kind `fleetinstance`），其中保存了 harness 已验证的路由与身份标识。客户端不再有实例文件：客户端总是有自己的本地 harness，因此首次连接前无需记住任何东西。

已注册的 peer 只需名称：

```bash
./bin/niffler-tui --instance-probe worker
./bin/niffler-tui --instance worker
```

尚未注册的 peer 在本次启动时给出路由：

```bash
./bin/niffler-tui --instance worker --ssh ubuntu@worker
./bin/niffler-tui --instance worker --incus worker [--host operator@hypervisor]
./bin/niffler-tui --instance worker --lima worker
./bin/niffler-tui --instance worker --bus nats://127.0.0.1:4222
```

每次启动只能有一个路由，身份由实时目录得知——命令行参数从不声明身份。guest 默认根为 `/home/ubuntu/niffler`；SSH 目标可用 `--instance-root` 覆盖。要在不带参数时看到 peer，请在 harness 上注册它（`fleet_install` 或 `fleet_instance_import`），它会出现在 `/niffler` 与环形切换中。

环形切换按注册表轮转：Alt+N / Alt+Shift+N 会跳过连接失败的 peer，报告并尝试下一个；全部失败时停留在原实例并明确提示。

Incus 收据（`transport: "incus"`）通过 incus exec 和原生 relay 以普通 ubuntu 用户连接，不用 guest SSH。根为 `/home/ubuntu/niffler`，明确选择本地 daemon 和 default 项目，忽略客户端可变默认值。Incus/Lima 可带 `host: "gokr@wowbagger.krampe.se"`，管理命令经该主机 SSH 执行，每个参数都做 shell 引用；省略则本地管理 guest。

管理 API/NATS 无需公开暴露。SSH 密钥和 known_hosts 属于客户端，不转发 agent、不复制 guest 密钥。部署/更新是经批准的框架 fleet 操作，不是 TUI 启动副作用。

### Switching between instances

访问过的每个实例保持独立连接，切换在进程内完成，不重新执行；离开实例上的工作继续运行。

| 输入 | 效果 |
|---|---|
| `Alt+N` / `Alt+Shift+N` | 下一个 / 上一个实例，循环切换 |
| `/niffler` | 列出实例和状态 |
| `/niffler NAME` | 切换到已注册的 NAME |
| `/niffler next` / `previous` | 通过命令进行相同切换 |

每个实例独立保存会话、工作区、草稿、滚动位置和连接；同一 session ID 不被假定在另一个实例中代表相同会话。底部标记当前和已访问实例：

```
● local   ○ worker ✓2   Alt+N next · Alt+Shift+N prev · /niffler
```

`●` 为当前屏幕实例，`✓N` 为离开时完成的会话数量，`!approval` 为等待人工，`…`/`⚠` 为连接中/断开。`/niffler local` 返回本机，但会核对 core 根目录，避免误接其他实例。未查看实例的审批仅显示，不代为批准。

### Completion notices

其他实例的轮次完成会增加未读标记；打开会话后清除。读取状态只保存在客户端，与代理通知确认独立；查看完成会话既不消费模型待处理通知，也不启动新轮次。同一会话多次完成合并为一个未读标记。

连接和重连时分页读取框架持久完成快照，配合实时推送，避免离线期间完成丢失。被终止的轮次或记录写入失败可能没有收据；没有收据表示未知，不表示成功。

### Remote specifics

要求框架 SDK 支持 `sdk.NewWithURL` 和 `Component.OnReconnected`；旧 SDK 编译失败而不是静默退化。

远程工作区默认收据根，而非笔记本 cwd。会话/重启状态按稳定 instance ID 而非转发端口保存，隧道换端口不影响历史。远程禁用本地路径补全和记录文件打开；附件仍来自明确选择的本地文件。每个转发连接在传送 SDK 流量前重新解析并验证固定目录身份，重连也如此；不匹配关闭该连接，不回退。这是独立探测连接，不是对恶意端点替换的身份认证。`/restart` 保留当前实例。后台审批尚未实现。

## Architecture

Niffler 的 Bubble Tea 终端客户端。它与桌面 UI 一样是总线客户端，通过 `svc.core.call` 驱动 session 调用，渲染 `ev.session.*` token、工具调用、助手回复和完成事件。

组件以零工具注册；运行客户端不向 LLM 工具上下文添加内容。
