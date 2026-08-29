# niffler-tui

[English](README.md) · 简体中文

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

- `Enter`：发送当前输入
- `Alt+Enter` / `Ctrl+J`：在输入里插入换行（多行消息）
- `Up` / `Down`：在输入内移动；在首行/末行时按 readline 方式浏览已发送
  消息历史，恢复进行中的草稿
- `Ctrl+R`：反向历史搜索；键入过滤，再按 `Ctrl+R` 看更早的匹配，
  `Enter` 接受，`Esc` 取消
- `PgUp` / `PgDn`：翻页浏览记录
- `Ctrl+Up` / `Ctrl+Down`：逐行滚动记录
- `Ctrl+T`：展开/收起所有 tool-run 卡片
- 鼠标滚轮：滚动记录
- `Ctrl+C`：退出

同一回合里连续的工具调用折叠成一张可收起的“tool-run card”，带摘要行
（✓/⚠ 符号、调用次数、名称），默认收起，长工具序列不碍眼。`Ctrl+T`
展开或收起每张卡片。鼠标追踪**默认开启**：滚轮滚动记录，左键点击切换
光标下的卡片。因为追踪会捕获点击，复制/粘贴选择用 `Shift`+拖拽（SGR
鼠标的标准方式）。`/mouse off` 完全关闭追踪——原生拖拽复制恢复，但应用
收不到滚轮事件，记录改用 `PgUp`/`PgDn`/`Ctrl+Up` 滚动，滚轮退化为普通
终端滚动。

本地命令由 TUI 处理，绝不发给模型：

- `/provider` —— 可搜索的全局提供商选择器；也提供环境回退与提供商设置
- `/model` —— 可搜索的当前提供商模型目录；选中立即持久化为本会话的
  覆盖（无需推理调用）
- `/connect` —— 掩码的提供商连接表单，使用 models.dev 模板或自定义
  OpenAI 兼容端点
- `/status` —— 详细的有效提供商/模型/上下文来源与用量
- `/mouse [on|off]` —— 鼠标追踪（默认开：滚轮+点击可用，复制用
  Shift+拖拽；关闭后恢复原生拖拽复制，但应用收不到滚轮）
- `/help` —— 命令摘要

选择器用 `/` 过滤、方向键移动、`Enter` 选中、`Esc` 返回聊天。提供商
表单会掩码 API key 字段；斜杠命令和凭据不会写入已发送消息历史或记录。

第二行头部始终显示有效的提供商与模型，外加上下文占用条。上下文占用
在可用时使用模型上报的总 token 数，经 Niffler 的会话元数据跨会话恢复
存续，并在与 core 相同的 75% 警告 / 90% 裁剪阈值处变色。提供商选择
改变 Niffler 的全局默认；模型选择仅限当前会话。

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
