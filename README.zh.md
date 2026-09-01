# niffler-tui

[English](README.md) · [简体中文](README.zh.md) · [繁體中文](README.zh-TW.md) · [Discord](https://discord.gg/ThJFEAJUAk)

Niffler 的 Bubble Tea 终端客户端。和桌面 UI 一样，它是个总线客户端：
经 `svc.core.call` 驱动 `session` 调用，并渲染 `ev.session.*` 的 token、
tool-call、assistant 与 completion 事件。

该组件以零工具注册。运行它不会给任何 LLM 的工具上下文添加东西。

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
- `Ctrl+E`：循环 tool-run 卡片可见性（`brief` → `full` → `off`）
- `Ctrl+G`：轮换本会话的 LLM 思考力度（`auto` → `low` → `medium` →
  `high`）
- `Esc`：回合运行中，第一次按下进入 “Stop?” 待命，第二次强制取消当前
  回合
- 鼠标：滚轮滚动记录；直接拖拽选择并复制；左键点击切换 tool-run 卡片
- `Ctrl+C`：退出

同一回合里连续的工具调用折叠成一张可收起的“tool-run card”，带摘要行
（✓/⚠ 符号、调用次数、名称），默认收起，长工具序列不碍眼。`Ctrl+E`
在 brief（收起）、full（全部展开）、off（隐藏）之间循环；左键点击切换
光标下的卡片。鼠标追踪**默认开启**：滚轮滚动记录，直接按住左键拖拽会
由应用选择并复制文本，两者可以同时使用。`/mouse off` 仍可切换到终端
原生选择，但应用会收不到滚轮事件，记录改用
`PgUp`/`PgDn`/`Ctrl+Up` 滚动，滚轮退化为普通终端滚动。

### 思考

模型推理以灰色斜体显示在每条 assistant 回复上方，按回合放置在记录中。
`Ctrl+T` 循环显示量：`full`（全部）、`brief`（每个块一行暗色
`▸ thinking…`）、`off`（完全隐藏）。推理在显示时被压缩：修剪边缘换行，
内部空行串折叠为单个换行，段落紧凑排列，不再堆成一片空白。

`Ctrl+G` 轮换本会话的 LLM 思考*力度*——`auto`（提供商默认）→ `low` →
`medium` → `high`。该选择与模型覆盖一样按会话持久化，在回合之间生效，
仅在设置时以 `reasoning_effort` 转发给 LLM，因此不支持思考力度的提供商
永远不会见到它。当前选择显示在头部的 `effort:auto|low|medium|high` 中。

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
- `/status` —— 详细的有效提供商/模型/上下文来源与用量
- `/new [id]` —— 开始一个新会话
- `/session` —— 会话浏览器；切换或恢复会话，或开始新会话
- `/locale [en|zh|zh-TW]` —— 切换界面语言（持久化）
- `/mouse [on|off]` —— 鼠标追踪（默认开：滚轮、点击和直接拖拽选择可
  同时使用；关闭后使用终端原生选择，但应用收不到滚轮）
- `/help` —— 命令摘要，含已注册的插件命令

Tab 补全适用于命令名，以及命令声明的参数值（内联候选，或经声明的来源
工具惰性获取）。未知命令会提示相近的命令名。

选择器用 `/` 过滤、方向键移动、`Enter` 选中、`Esc` 返回聊天。提供商
表单会掩码 API key 字段；斜杠命令和凭据不会写入已发送消息历史或记录。

头部显示会话 id、`think:`/`tool:`/`effort:` 状态块，以及有效的提供商与
模型，外加上下文占用条。上下文占用在可用时使用模型上报的总 token 数，
经 Niffler 的会话元数据跨会话恢复存续，并在与 core 相同的 75% 警告 /
90% 裁剪阈值处变色。提供商选择改变 Niffler 的全局默认；模型选择仅限
当前会话。

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
