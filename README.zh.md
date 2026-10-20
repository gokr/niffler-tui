# niffler-tui

[English](README.md) · [简体中文](README.zh.md) · [繁體中文](README.zh-TW.md) · [Discord](https://discord.gg/ThJFEAJUAk)

> 🤖 AI 自动翻译；以 [English](README.md) 为准。

[Niffler](https://github.com/gokr/niffler) 的终端聊天客户端。Niffler 是能够自我扩展的编程代理框架。在终端中与代理对话、查看工具执行过程，并继续之前的会话。

- 流式 Markdown 回复、推理内容和可折叠工具卡片。
- 会话历史、可搜索的会话列表及提供商/模型选择器。
- 文件附件、选中复制和审批提示。
- 多种主题，以及英语、简体中文和繁体中文界面。

## Get started

在 **Niffler 框架仓库**中安装客户端和启动器：

```bash
make install WITH_TUI=1
```

在 Niffler 中配置模型提供商，然后在真实终端中进入需要代理操作的项目：

```bash
cd /path/to/your/project
niffler-tui
```

安装的启动器会在需要时启动本地框架。输入请求，按 `Enter` 发送。当前目录成为新会话的工作区；如有该框架/工作区的已保存会话，会自动恢复。
使用 `/new` 新建会话，或用 `/session` 浏览历史会话。

尚未配置提供商？使用 `/connect` 填写连接信息，或用 `/provider` 选择已有提供商或订阅登录。`/model` 为当前会话选择模型。连接表单中的凭据会被遮蔽，不写入聊天历史。

## Everyday controls

| 按键 / 命令 | 操作 |
|---|---|
| `Enter` | 发送；忙碌时引导当前轮次 |
| `Alt+Enter` / `Ctrl+J` | 换行 |
| `Tab` | 补全命令与参数 |
| `Up` / `Down`、`Ctrl+R` | 回顾或搜索输入历史 |
| `PgUp` / `PgDn` | 滚动会话 |
| `Ctrl+T` / `Ctrl+E` | 调整推理 / 工具卡片详细程度 |
| `Esc`，再按 `Esc` | 确认停止当前轮次 |
| `/new`、`/session` | 新建或切换会话 |
| `/theme`、`/locale` | 修改主题或语言 |
| `/help` | 查看内置及插件命令 |
| `Ctrl+C` | 退出客户端 |

鼠标滚轮可滚动；拖动可选中并复制文本。受保护的代理操作需要审批。退出客户端不会停止正在执行的代理轮次。

## Learn more

**[Manual](MANUAL.zh.md)**提供完整按键和命令参考、配置、shell 转义、客户端状态迁移、开发说明，以及实验性的多实例/fleet 支持。

Linux 和 macOS 上的客户端偏好及状态保存在 `~/.niffler/`；会话记录保存在框架 store 中。详见 [client state](MANUAL.zh.md#transcript-and-client-state)。

## Build from source

通过本地 Go SDK replacement 关联匹配的 Niffler 仓库后：

```bash
make build      # bin/niffler-tui
make test
```

先启动框架，再从终端运行 `./bin/niffler-tui`。仅构建二进制文件不会安装自动启动/重启启动器。参见 [Build](MANUAL.zh.md#build) 和 [plugin installation](MANUAL.zh.md#plugin-status)。
