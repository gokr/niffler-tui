# niffler-tui

[English](README.md) · [简体中文](README.zh.md) · [繁體中文](README.zh-TW.md) · [Discord](https://discord.gg/ThJFEAJUAk)

> 🤖 AI 自动翻译，可能滞后于英文版；以 [English](README.md) 为准。

Niffler 的 Bubble Tea 终端客户端。和桌面 UI 一样，它是个总线客户端：
经 `svc.core.call` 驱动 `session` 调用，并渲染 `ev.session.*` 的 token、
tool-call、assistant 与 completion 事件。

该组件以零工具注册。运行它不会给任何 LLM 的工具上下文添加东西。

## Fleet startup (experimental)

可从 harness 的原生 Nim `fleet` 组件导入私有部署回执，随后探测或启动实例：

```bash
./bin/niffler-tui --instance worker --instance-import /path/to/worker.json
./bin/niffler-tui --instance-probe worker
./bin/niffler-tui --instance worker
```

导入先验证在线 catalog，再将 0600 配置保存到
`$XDG_CONFIG_HOME/niffler/instances.json`（默认 `~/.config/niffler/`）。
SSH 使用正常配置和 known_hosts，禁用 agent 转发；Lima 使用原生 `limactl shell --workdir=/home/ubuntu` 和
工作节点的原生 `<receipt-root>/var/bin/cli relay <port>`，不提取密钥、不挂载
主机、不开放公共监听。请先重新构建或更新工作节点的 harness：CLI 必须支持
`relay`（POSIX，仅限回环）；缺失或过旧会拒绝，不回退。无需 Python 运行时
或传输辅助程序。不启动或停止远端服务。
目标缺失或身份不符会拒绝，不回退本地。需配套 harness 包装器，以避免远端
参数触发本地自动启动；普通启动不变。

Incus 回执（`transport: "incus"`）通过 `incus exec` 和原生 relay，以客体普通
用户 `ubuntu` 连接，无需客体 SSH。Incus 根目录固定为 `/home/ubuntu/niffler`。
Incus 命令明确选择本地 daemon 和 `default` 项目，忽略客户端默认远端/项目。
Incus 和 Lima 回执可包含 `host: "gokr@wowbagger.krampe.se"`，通过 SSH
在该主机上执行命令，并逐参数进行 shell 引用；本地客体省略 `host`。
无需公开管理 API 或 NATS 监听，不转发 agent，也不复制客体密钥。
部署和更新属于需批准的 harness fleet 操作，不是 TUI 启动的副作用。

### Switching between instances

一个客户端会为每个访问过的实例保持一条活动连接，切换在**进程内**完成：
不重新执行进程，你离开的实例上的工作继续运行。

| 输入 | 作用 |
|---|---|
| `Alt+N` / `Alt+Shift+N` | 下一个 / 上一个实例（循环） |
| `/niffler` | 列出实例及其状态 |
| `/niffler NAME` | 切换到 NAME（需先注册） |
| `/niffler next` / `previous` | 同样的循环切换 |

每个实例各自保留会话、工作区、草稿、滚动位置和连接，互不共享；一个实例上的
会话 ID 绝不会被当作另一个实例上的同一会话。底行显示当前实例和所有访问过的
实例及其状态：

```
● local   ○ worker ✓2   Alt+N next · Alt+Shift+N prev · /niffler
```

`●` 表示当前显示的实例，`✓N` 是你不在时完成的会话数，`!approval` 表示有人在
等待批准，`…`/`⚠` 表示仍在连接 / 已断开。`/niffler local` 返回本机 —— 会用
本地根目录校验应答的 core，而不是盲目信任，因此返回绝不会连上陌生的总线。
未在查看的实例上的批准会显示，但绝不会替你应答。

### Completion notices

在别的实例上完成的回合会点亮该实例的未读标记；打开该会话即清除。已读状态
保存在客户端本地，与 agent 的 notice 确认相互独立：打开已完成的会话既不会
消耗模型仍需要的东西，也不会触发新回合。同一会话的多次完成会合并为一个未读
标记。

实时推送覆盖常态；在连接时和每次重连之后，客户端会分页读取 harness 的持久
完成快照，因此客户端不在时发生的完成不会丢失。被终止的回合或写入失败可能
完全没有回执：缺失表示未知，绝不表示成功。

### Remote specifics

远端工作区默认回执根目录，不使用本机 cwd。上次会话和重启状态使用稳定实例
ID，不使用转发端口，因此新的隧道端口不会丢失历史。远端禁用本地路径补全和
记录文件打开；附件仍由明确选择的本地文件提供。每次新建转发连接（含重连）都
重新解析端点并验证固定 catalog 身份；不符会关闭该通道，不回退。验证使用独立
探测连接，不能抵御恶意端点在探测后切换。`/restart` 现在会留在你所在的实例。
尚无后台批准。

## 构建

期望把本仓库 checkout 在 Niffler 旁边，这样本地 `go.mod` 的 replacement
能找到 Go SDK：

```bash
make            # 构建 bin/niffler-tui
make test       # 跑 TUI 测试套件
make run        # 构建 + 运行
```

或直接：

```bash
go build -o bin/niffler-tui ./tui
```

Niffler 的 builder 不使用这个 `go.mod`；它创建隔离的模块，replacement
指向它自己的 `sdk/go` 目录。

## 运行

启动 Niffler，然后从真实终端运行客户端：

```bash
./bin/niffler-tui
./bin/niffler-tui -session game
```

配置：

- `-session <id>` 选择会话，默认依次取 `NIF_SESSION`、`console`。
- `NIF_NATS_URL` 选择总线。未设置时，客户端依次检查
  `$NIF_ROOT/var/nats-url`、`./var/nats-url`，最后是
  `nats://127.0.0.1:4222`。
- 界面语言依次由 `/locale` 持久化的选择、`NIF_TUI_LOCALE`
  （`en`/`zh`/`zh-TW`）、`LANG`/`LC_ALL`（zh_TW/zh_HK → 繁体，其他 zh →
  简体）决定，最后回退英文。

按键：

- `Enter`：发送当前输入；回合运行中按 Enter 则“转向”（steer）——该消息
  以 “Steer: …” 行折入正在运行的回合
- `Alt+Enter` / `Ctrl+J`：在输入里插入换行（多行消息）
- `Up` / `Down`：在输入内移动；在首行/末行时按 readline 方式浏览已发送
  消息历史，恢复进行中的草稿
- `Ctrl+R`：反向历史搜索；键入过滤，再按 `Ctrl+R` 看更早的匹配，
  `Enter` 接受，`Esc` 取消
- `Tab` / `Shift+Tab`：补全光标下的斜杠命令或参数——输入上方出现一条
  暗色候选行，`Tab`/`Shift+Tab` 循环高亮候选，`Esc`（或任意其他键）
  取消，仅有一个候选时直接填入
- `PgUp` / `PgDn`：翻页浏览记录
- `Ctrl+Up` / `Ctrl+Down`：逐行滚动记录
- `Ctrl+T`：循环思考可见性（`full` → `brief` → `off`）
- `Ctrl+E`：循环 tool-run 卡片细节（`brief` → `medium` → `full` → `off`）
- `Ctrl+O`：worker 循环——运行中的后台进程与工作中的子代理，每个 worker
  一张实时卡片（状态、时长、正在运行的工具、最后一行输出）；没有运行时
  回答 "nothing is running (bg/agent)"
- `Shift+Tab`：轮换本会话的 LLM 思考力度（`auto` → `low` → `medium` →
  `high` → `max`）
- `Esc`：回合运行中，第一次按下进入 “Stop?” 待命，第二次强制取消当前
  回合
- 鼠标：滚轮滚动记录；直接拖拽选择并复制；左键点击切换 tool-run 卡片；
  ctrl+点击打开指针下的网址或文件路径；alt+拖拽额外将选区保存为片段
- `Ctrl+X`：轮换收藏的提供商/模型组合（`/fav` 管理；回合之间生效，同 `/model`）
- `Ctrl+C`：退出

同一回合里连续的工具调用折叠成一张可收起的“tool-run card”。默认的
`medium` 级别下，每次调用按工具类型渲染：bash 显示 `$ command`、输出
末尾几行以及 `...（之前 N 行，ctrl+e 展开）` 标记和退出状态 + 耗时；edit
显示旧/新文本的红绿 diff 与 `+A -R` 行数；read/write 显示文件头部并提示
折叠行数；其他工具回退为名称、参数和结果头部。`brief` 把每张卡片压缩为
一行摘要（✓/⚠ 符号、调用次数、名称），适合长工具序列；`full` 不再折叠
展开每次调用的输出；`off` 完全隐藏。`Ctrl+E` 循环各级别，左键点击切换光标
下的卡片（该卡片 brief→medium→full）。鼠标追踪**默认开启**：滚轮滚动记录，
直接按住左键拖拽会由应用选择并复制文本，两者可以同时使用。
两个修饰键手势复用同一套选择机制。**ctrl+点击**会打开指针下的内容：网址
交给浏览器；文件路径交给系统默认处理器（可带 grep 风格的 `:line` 后缀，
会被剥掉），并按对话工作目录、启动目录和 harness 根目录依次解析
（`NIF_TUI_OPEN` 可整体替换打开器，如 `NIF_TUI_OPEN='kitty nvim'`）。
**alt+拖拽**在复制之外额外把选区保存为一个*片段*（snippet）。片段存放
在 store 中（kind `snippet`），带有收集时的对话与时间戳；`/snippets`
列出收藏，模型也可以通过 store 自己的工具读取同样的文档，因此会话双方
之后都能取回片段。两个手势都需要终端上报鼠标修饰键（SGR 鼠标编码——
与拖拽选择所需的 xterm 兼容终端相同）。`/mouse off`
仍可切换到终端原生选择，但应用会收不到滚轮事件，记录改用
`PgUp`/`PgDn`/`Ctrl+Up` 滚动，滚轮退化为普通终端滚动。

### 思考

模型推理以灰色斜体显示在每条 assistant 回复上方，按回合放置在记录中。
推理也会经过 markdown 渲染（披上思考色调）：正文保持暗色斜体，而推理中
的围栏代码块保留语法高亮。`Ctrl+T` 循环显示量：`full`（全部）、`brief`
（每个块一行暗色 `▸ thinking…`）、`off`（完全隐藏）。推理在显示时被压缩：
修剪边缘换行，内部空行串最多折叠为一个空行，段落间隔保留，又不会堆成
一片空白。

`Shift+Tab` 轮换本会话的 LLM 思考*力度*——`auto`（提供商默认）→ `low` →
`medium` → `high` → `max`。该选择与模型覆盖一样按会话持久化，在回合之间
生效，仅在设置时以 `reasoning_effort` 转发给 LLM，因此不支持思考力度的
提供商永远不会见到它。各提供商的级别并非统一标度——例如 DeepSeek 将
`minimal`/`low` 映射为 `low`、`medium`/`xhigh` 映射为 `high`、`max`
映射为 `max`——因此 `max` 是提供商暴露的、高于 `high` 的唯一一档。当前
选择显示在头部的 `effort:auto|low|medium|high|max` 中。

本地命令由 TUI 处理，绝不发给模型：

- `/provider` —— 可搜索的全局提供商选择器（e：编辑，d：删除）；也提供
  环境回退、提供商设置与订阅登录（ChatGPT Plus/Pro 支持浏览器或设备码，
  Claude Pro/Max 支持浏览器）。OAuth 流程会打开授权 URL，轮询直到
  Niffler 的 provider 组件存入令牌，本地回调端口不可用时可粘贴
  授权码/跳转 URL；esc 取消
- `/model` —— 可搜索的当前提供商模型目录；选中立即持久化为本会话的
  覆盖（无需推理调用）
- `/connect` —— 掩码的提供商连接表单，使用 models.dev 模板或自定义
  OpenAI 兼容端点
- `/mcp [add|edit|on|off|refresh|search]` —— 管理外部 MCP 服务器；不带参数的
  `/mcp` 打开服务器浏览器
- `/lsp [add|edit|remove]` — 管理语言服务器注册表（配置即支持：为 .go 注册 gopls 后，lsp 工具即获得诊断与精确导航）；裸 `/lsp` 打开服务器浏览器
- `/processes` — 查看并管理后台进程（通过 bash 的 run_in_background 或
  process_start 启动的开发服务器、监视器）：有进程运行时状态栏显示 `bg N`
  徽标；enter 查看输出尾部（不影响模型的增量读取游标）；d 二次确认后终止
- `/status` —— 详细的有效提供商/模型/上下文来源与用量
- `/info [id]` —— 基于 store 消息文档的会话统计：各角色消息数、每个工具的调用
  次数与结果大小（总量、中位数、最大值，附最大的几条及预览）、token 用量合计，
  以及 discover/invoke 的使用分布。默认统计当前会话；给 id 可查看其他会话
  （tab 补全），子代理会显示其父会话
- `/name [名称]` —— 设置本会话的名称（头部标题，替换自动截断的首条消息标题）；
  不带参数显示已存的名称
- `/copy` —— 把最后一条助手回复复制到剪贴板（OSC 52，与拖拽选择同一通道）
- `/fav [add|rm <n>|<n>]` —— 收藏提供商/模型组合，存为一个有序的 store 文档
  （kind `modelfavorite`）；`<n>` 把某条套用到本会话，`Ctrl+X` 按顺序轮换到
  下一条（循环；回合之间生效，同 `/model`）
- `/doctor [deep]` —— 以 Markdown 表格显示健康检查；`deep` 运行实时探测
- `/export [path]` —— 导出当前发送给提供商的 JSON（美化缩进）；不带路径时在对话中显示（有截断），带路径时相对于对话工作目录写入文件
- `/compact` —— 立即运行可替换的压缩器（不产生 LLM 回合）：较早的历史被替换为经验证的检查点，长对话可在触发自动压力阶梯前主动压缩；压缩器拒绝时会明确报告，绝不会静默降级为有损裁剪
- `/approvals [ask|auto]` —— 本对话的批准闸门：`auto` 对所有 `x-harness.approval` 工具免询问放行（每次放行都会明确记录，静默放行正是闸门要防止的）；`ask` 恢复询问，不带参数时报告当前模式。闸门弹窗中的 `A` 键可在弹窗出现时设置同样的模式：两段确认（先 `A`，再按 `A` 确认，与停止和终止的确认方式一致），并连同该对话已在队列中的请求一并放行。它也覆盖程序形态的调用（这是按工具放行的 `a` 刻意不做的）——因此最宽的放行需要多一次按键。`a` 的含义保持不变（本会话始终允许“该”工具）
- `/workspace [路径]` —— 本对话的工作位置，也是客户端替你固定的那一项的控制器：在某个目录启动时，首个回合会把它固定为对话工作区。不带参数的 `/workspace` 会报告路径、git 分支、该检出是否为**关联工作树**（LINKED worktree，而非主检出）以及主检出路径，还有已授予根目录的数量。带路径参数会**重新固定**工作区——仅在对话仍处于初始状态（pristine）时允许，因为一旦运行过回合，工作区即不可变：恢复必须按原始回合的方式解析上下文与路径，系统提示也据此冻结。超过该点后 core 会拒绝并说明原因，此时应新建对话。与 `/roots` 的区别：这里说的是主根（相对路径的解析基准），而 `/roots` 是在其之上授予*额外*根目录。底行会在分支旁用 `⧉` 标记关联工作树。
- `/roots [add|remove <目录>]` —— 本对话的工作集：直接输入 `/roots` 会报告主工作区（不可变，在对话诞生时固定）以及在其之上授予的每个根目录；`add` 与 `remove` 用来更改授予；底行会显示分支以及 `+N` 计数，让授予就显示在工作区本来就在的位置。授予会扩大工具接受范围与写入围栏的准入范围，因此会一直保留到你移除为止——它不是操作系统级沙箱。`~` 会被展开，相对路径按对话工作区解析（core 要求绝对且存在的目录）：`/roots add ~/git/另一个检出`
- `/components [all|direct|discovered|undiscovered]` —— 本会话实际可调用的工具，
  按组件列出，并显示每个工具的暴露状态
- `/discover <组件>|tool=名称` —— 将某个组件的工具（或指定的单个工具）暴露给
  本会话，使模型可以直接调用它们
- `/recall <词…>` —— 搜索本会话的全部历史（经由 `context_recall` 的排序
  全文检索通道，包括已被修剪或压缩掉的片段），显示带上消息 id 的有界单行命中。
- `/snippets [show|ins|del <n>]` —— alt+拖拽收集的片段列表（1 = 最新）。
  `show` 显示全文，`ins` 插入输入框，`del` 删除一条。片段以 store 文档
  （kind `snippet`）存放，模型也能通过 store 的工具取回它们
- `/agent [名称|none]` —— 新对话出生时的 agent 定义：它说话所用的 persona、起始工具集，
  以及随之冻结的模型、思考深度和预算。`none` 清除选择，不带参数的 `/agent` 打开选择器，
  显示每个定义的组成。与 `/toolset` 一样是客户端状态：选择只对之后新建的对话生效，
  绝不改写已存在的对话（harness 会拒绝——身份在出生时固定）。当前选择显示在标题栏
- `/skills [名称]` —— 浏览 harness 上已安装的技能并加载其中一个。不带参数的 `/skills` 打开菜单，列出每个技能的来源（`project`/`bundled`/`home`/`config`）和描述；本对话已加载的技能会标上 `● ` 并排在前面，让「已在上下文中」可见而不是靠记忆。选中即加载，`/skills <名称>` 则不经菜单直接加载。列表来自 skills 组件的 `skill_list`，加载走的是模型同一个 `skill_load`，因此大小上限与发现/遮蔽规则只有一处实现——此命令取代组件自带的原始 JSON 列表，后者既不能显示已加载状态，也不提供加载方式。
- `/toolset [名称|default]` —— 应用于新会话的工具配置；`default` 清除它，
  不带参数的 `/toolset` 打开选择器，列出已存储的配置，标出当前选择与每个
  配置解析出的工具数。选择器中的 `＋ New profile…` 打开表单，在存储前
  对照实时目录校验每个选择器：裸选择器命名组件，单个工具写作
  `component.tool`（如 `edit.read`，而非 `read`），`-tool` 表示排除。
  这里没有安全边界——配置只为在其被选中期间新建的会话裁剪或添加工具。
- `/new [id]` —— 开始一个新会话
- `/session` —— 会话浏览器；切换或恢复会话，或开始新会话。在过滤框中输入会对
  harness 的 store 执行**服务端搜索**（`search`，gokr/niffler#77）——按 id 和
  标题在数据所在之处匹配会话，而不是把全部会话下载到浏览器再过滤；若 store
  没有该工具（旧版 harness），则退回对已加载列表做本地过滤
- `/locale [en|zh|zh-TW]` —— 切换界面语言（持久化）
- `/theme [name]` —— 切换界面配色主题（持久化）；不带参数的 `/theme` 打开
  选择器，移动选择即可实时预览。除编译内置的 ANSI 默认主题外，另附十七套配色，
  含适合白底终端的浅色主题（`light`、`sepia`、`solarized-light`、`github-light`、
  `rose-pine-dawn`），以及 `solarized-dark`、`gruvbox-dark`、`nord`、`dracula`、
  `tokyo-night`、`catppuccin-mocha`、`monokai`、`one-dark`、`github-dark`、
  `rose-pine`、`kanagawa`、`everforest`。`NIF_TUI_THEME` 可覆盖启动默认值
- `/mouse [on|off]` —— 鼠标追踪（默认开：滚轮、点击和直接拖拽选择可
  同时使用；关闭后使用终端原生选择，但应用收不到滚轮）
- `/cards [on|off]` —— 用主题的卡片底色渲染工具运行块，使其读作一个整体
  （仅本次运行的显示开关）
- `/niffler [NAME|next]` — 列出或重新执行切换固定身份的远端实例；验证失败则留在原处。返回隐式本地实例须退出后普通启动。
- `/restart` —— 以保留的退出码退出，让已安装的 `niffler-tui` 包装脚本重新运行
  二进制，从而在更新后加载新构建的安装；不通过包装脚本运行时则直接退出。
  你所在的会话和此客户端的身份（其 `Niffler N` 标签）会移交到继任进程，
  即使旧进程从未释放该会话，继任者也会恢复它
- `/quit` —— 退出客户端，与 ctrl+c 相同；客户端身份正常释放（不写重启移交
  记录），仍在运行的回合会继续在其会话运行器中执行，下次连接时从存储恢复
- `/alias <名称> <提示…>` —— 定义提示快捷键：输入 `/名称`（其后可追加文字，会
  接到已存储的提示之后）会把该提示作为普通回合发送。改用
  `/alias add <名称> cli call <工具> '<json>'` 可定义调用别名，直接调用某个工具，
  其中 `$1`…`$9` 会代入调用时提供的参数。不带参数的 `/alias` 或
  `/alias list` 列出已定义别名，`/alias rm <名称>` 删除；定义持久化到用户状态
  目录，支持 Tab 补全并出现在 `/help` 中。内置命令名不可被占用
- `/help` —— 命令摘要，由已注册的命令表生成，因此始终列出每个内置命令
  （以及每个插件命令）

Tab 补全适用于命令名，以及命令声明的参数值（内联候选，或经声明的来源
工具惰性获取）。未知命令会提示相近的命令名。

### Shell 转义

两个 shell 前缀会在你自己的 shell 中运行命令——`$SHELL -c`，在对话的
工作目录下，使用本进程的环境并设置 `NIF_TUI=1`——并把输出以暗色块显示在
记录中。它们是用户自己的命令行，因此无需连接、不触发批准闸门（批准闸门
针对*模型*的工具调用，而非你的按键），也不经过 harness；不支持交互式
程序（`vim`、`less`、`ssh` 等），因为命令没有连接终端。在远程
`NIF_NATS_URL` 下，命令运行在 TUI 所在的机器上，而非 harness 的机器上。

- `!<命令>` — 输出只留在本地，等同于在另一个终端里输入
  （`!git status`、`!make test`）。
- `?<命令>` — 运行同一个命令，然后把输出作为一轮对话发给模型：
  `?git status` 本地运行并就结果提问。若已有回合在运行，输出会改为注入
  该回合；若命令运行期间切换了会话，该提示会被丢弃（输出仍然可见，可以
  再次提问）。

输出上限为 8 KB，超出时明确标注 `… N bytes truncated`；超时会终止挂起的
命令。单独的 `!` 或 `?` 按普通提示词处理；除此之外前缀始终优先于正文：
以 `!` 或 `?` 开头的行一律视为转义，因此想交给模型的句子不要以此开头。

`?` 也适用于本地命令本身：`?/status`、`?/doctor deep`、`?/recall <词>`、
`?/info`、`?/components` 等会与 `/命令` 完全一样地运行该命令，然后把它的
输出发给模型（同样有上限）。这是询问命令输出的唯一方式——各命令上的 `ask`
参数（`/status ask`、`/recall … ask`、`/doctor … ask`）已移除。会打开选择器
或表单的命令（`/session`、`/theme`、`/profile`）没有可询问的输出，会拒绝该
前缀；`!` 也绝不会运行斜杠命令。

选择器用 `/` 过滤、方向键移动、`Enter` 选中、`Esc` 返回聊天。提供商
表单会掩码 API key 字段；表单中的凭据不会写入已发送消息历史或记录。
提交的行（提示词与斜杠命令同样）都会进入按会话的输入历史，因此
上箭头和 ctrl+r 也能找回 `/status`、`/mcp search ...` 等命令。

头部显示会话 id、`think:`/`tool:`/`effort:` 状态块，以及有效的提供商与
模型，外加上下文占用条。上下文占用在可用时使用模型上报的总 token 数，
经 Niffler 的会话元数据跨会话恢复存续，并在与 core 相同的 75% 警告 /
90% 裁剪阈值处变色。同一行还会显示会话累计 token 用量（`↑ 输入 ↓ 输出`）与
prompt 缓存命中率（恢复会话时从对话头信息载入）。提供商选择改变
Niffler 的全局默认；模型选择仅限当前会话。

输入区上下各有分隔线；临时回合状态（spinner 与 `working`、停止中、两次
esc 停止提示）会以 Pi 风格嵌入上方分隔线开头几个字符处。底部只有一行，
显示对话工作目录和临时提示——命令与按键说明已移到 `/help` 和 Tab 补全。

## 插件斜杠命令

组件可以声明式扩展 TUI，无需任何 UI 代码：组件照常注册工具，并在注册
信息中添加 `slash` 段（`{name, description, tool, params[]}`，参数可带
补全来源；见 Niffler 仓库的 `docs/WIRE.md`）。core 校验该规格，把合并
后的命令表写入 store 检查点，并在每次变更时广播 `ev.catalog.updated`；
TUI 先读 store，再实时跟随该事件。

执行已注册命令时，TUI 按声明的参数解析命令行（位置参数、`name=value`、
裸布尔旗标、默认值）并调用目标工具；结果显示为记录中的 meta 块。内建
命令遮蔽同名注册，命令随其组件退出而自动消失。

Assistant 回复渲染为 Markdown（标题、粗体、斜体、列表、表格，以及经
Glamour 语法高亮的代码块）。token 流式期间，块以纯文本显示，输出暂停
后升级为带样式的 Markdown，避免每个 token 都做一次昂贵的完整 Markdown
渲染。

启动时以及每次切换会话后（`/session`、`/new` 或浏览器中的条目），输出
区域会根据会话存储的消息重建——用户与 Assistant 回合、推理内容、带参数
与结果的工具卡片分组、回合错误，以及折叠运行时通知的暗色 `▸` 机器行
（子代理完成、后台进程退出，或自主唤醒回合的提示）——因此之前的对话可见
且可滚动查看，而不再是一片空白。这些通知行也会实时到达：父会话因子代理
完成而被唤醒时，会显示结算通知及随后的回复，无需人工询问。状态行在子代理
工作时显示 `agent N (时长)`，在后台进程运行时显示 `bg N` 徽标。

视口只保留有限的滚动窗口（默认最近 3000 行渲染结果，更早的消息处以标记
提示），这是流式渲染开销不随会话增长的关键：已完成的块被缓存，只有流式
尾部重新渲染，token 重绘被合并，视口扫描量不超过窗口。可用
`NIF_TUI_SCROLLBACK` 设置保留行数，`0` 表示不限制。完整对话始终保存在
会话存储中。

历史按会话以 JSONL 持久化在
`$XDG_STATE_HOME/niffler-tui/history-<session>.jsonl`（或
`~/.local/state/niffler-tui/`），上限 200 条。删除该文件即清空。

Markdown 样式跟随 `GLAMOUR_STYLE` 环境变量（默认 `dark`；见 Glamour
的样式，如 `light`、`ascii`、`dracula`、`tokyo-night`）。

备用屏幕让视口与输入在 token 流式期间保持稳定。记录向上滚动后，新
的 token 不会强制把它拉回底部。

## 插件状态

`niffler.json` 把该组件标记为 `"interactive": true`：它应由插件安装
构建，但由用户在自己的终端里手动启动：

```bash
./var/bin/cli install gokr/niffler-tui
./var/bin/tui -session game
```

交互式组件不受 Niffler 监督或重启。更新或移除该包之前请退出正在运行
的 TUI。

### Lima migration

`lima` 使用原生 `limactl shell --workdir=/home/ubuntu NAME` 與 guest CLI stdio relay，root 固定 `/home/ubuntu/niffler`。支援可選 SSH host、直接 SSH 與 Incus。舊 Multipass 回執或 registry `multipass` 欄位明確拒絕；建立新 Lima guest 並匯入新驗證回執，勿更名舊路由或複製 instance-id。Fleet 固定 Ubuntu digest、停用主機掛載與憑證轉送及自動 port forwarding，啟用 guest 持久 user service。真實 Lima smoke 仍需另行人工授權。
