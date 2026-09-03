// UI localization for the TUI chrome. Model-facing strings (tool schemas,
// transcript labels like "tool>", backend error messages) deliberately
// stay English so model behaviour and logs are language-stable — only
// human-visible chrome lives here.
//
// Catalog discipline: the en catalog is the source of truth; zh and zh-TW
// must carry the identical key set — TestCatalogsComplete fails CI on
// drift (same contract as the desktop UI locales). {0} {1} placeholders
// are positional and filled by t(loc, key, vars...).
//
// Resolution order: a choice persisted by /locale, then NIF_TUI_LOCALE,
// then LANG/LC_ALL (zh_TW/zh_HK -> Traditional, other zh -> Simplified),
// then English.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Locale string

const (
	LocaleEN   Locale = "en"
	LocaleZH   Locale = "zh"
	LocaleZHTW Locale = "zh-TW"
)

var catalogEn = map[string]string{
	"status.ready":                   "ready",
	"status.connecting":              "connecting to {0}",
	"status.stopping":                "stopping…",
	"status.stopArmed":               "Stop?  (press esc again to cancel)",
	"status.working":                 "working",
	"status.updating":                "updating settings",
	"status.hint":                    "| /provider /model /connect /help | alt+enter: newline | ctrl+r: history | ctrl+e: tools | ctrl+t: think | ctrl+g: effort | esc: stop | ctrl+c: quit",
	"footer.filterChoose":            "/: filter  •  enter: choose  •  esc: back",
	"footer.filterSwitch":            "/: filter  •  enter: switch  •  e: edit  •  d: remove  •  esc: back",
	"footer.confirmRemove":           "Remove {0}? press d/x again to confirm, esc to cancel",
	"search.noMatch":                 "(no match)",
	"search.prompt":                  "(reverse-i-search)`{0}`: {1}",
	"selector.providers":             "Providers — global default",
	"selector.connectCatalog":        "Connect provider — choose a catalog template",
	"selector.sessionsLoading":       "Sessions — loading…",
	"selector.sessions":              "Sessions",
	"selector.models":                "Models",
	"selector.envDefault":            "Environment default (NIF_OPENAI_*)",
	"selector.fallback":              "fallback",
	"selector.connectProvider":       "+ Connect provider",
	"selector.connectProviderDesc":   "store an API key and OpenAI-compatible endpoint",
	"selector.customProvider":        "Custom OpenAI-compatible",
	"selector.customProviderDesc":    "configure nickname, endpoint, catalog and model manually",
	"oauth.option.openai.browser":    "Sign in with ChatGPT (Plus/Pro)",
	"oauth.option.openai.desc":       "subscription OAuth login — browser authorization",
	"oauth.option.openai.device":     "Sign in with ChatGPT (device code)",
	"oauth.option.openai.deviceDesc": "headless subscription login via a device code",
	"oauth.option.anthropic":         "Sign in with Claude (Pro/Max)",
	"oauth.option.anthropic.desc":    "subscription OAuth login — browser authorization",
	"oauth.title":                    "Sign in to {0}",
	"oauth.meta":                     "Subscription credentials are stored by Niffler and refreshed automatically; tokens never enter chat history.",
	"oauth.openHint":                 "Open this URL in your browser:",
	"oauth.deviceCode":               "Device code:",
	"oauth.waiting":                  "waiting for authorization…",
	"oauth.completing":               "completing authorization…",
	"oauth.callbackUnavailable":      "The local callback port is unavailable — after login, paste the final redirect URL below.",
	"oauth.manualPrompt":             "Code/URL",
	"oauth.manualPlaceholder":        "authorization code or redirect URL (optional)",
	"oauth.manualHint":               "enter: submit code/URL",
	"oauth.esc":                      "esc: cancel login",
	"oauth.keys":                     "esc: cancel  •  enter: submit code/URL",
	"oauth.signedIn":                 "signed in: {0} — tokens stored and auto-refreshed",
	"selector.catalog":               "catalog {0}",
	"selector.modelsCount":           "{0} models",
	"selector.connected":             "connected",
	"selector.providerDefault":       "provider default",
	"selector.useProviderDefault":    "Use provider default",
	"selector.reasoning":             "reasoning",
	"selector.tools":                 "tools",
	"selector.ctx":                   "ctx {0}",
	"selector.newSession":            "+ New session",
	"selector.newSessionDesc":        "start a fresh conversation",
	"form.nickname":                  "Nickname",
	"form.nicknameLocked":            "Nickname (locked)",
	"form.baseUrl":                   "Base URL",
	"form.catalogId":                 "Catalog ID",
	"form.model":                     "Model",
	"form.context":                   "Context",
	"form.apiKey":                    "API key",
	"form.connectTitle":              "Connect provider — {0}",
	"form.editTitle":                 "Edit provider — {0}",
	"form.connectMeta":               "OpenAI-compatible endpoint; credentials are stored by Niffler and never added to chat history.",
	"form.editMeta":                  "Editing {0}; leave API key blank to keep the stored credential.",
	"form.saving":                    "saving provider…",
	"form.keys":                      "tab/shift+tab: field  •  enter: next/save  •  ctrl+s: save  •  esc: cancel",
	"form.apiKeyRequired":            "API key is required (use a placeholder for a keyless local endpoint)",
	"form.leaveBlankToKeep":          "leave blank to keep",
	"form.oauthLeaveBlank":           "OAuth login — leave blank to keep tokens",
	"form.prompt.nickname":           "Nickname   ",
	"form.prompt.baseUrl":            "Base URL   ",
	"form.prompt.apiKey":             "API key    ",
	"form.prompt.catalog":            "Catalog ID ",
	"form.prompt.model":              "Model       ",
	"form.prompt.context":            "Context     ",
	"form.placeholder.apiKey":        "required",
	"form.placeholder.catalog":       "models.dev provider id (optional)",
	"form.placeholder.model":         "auto-picked from catalog; override if you like",
	"form.placeholder.context":       "0 = auto",
	"form.nicknameRequired":          "nickname is required",
	"form.baseUrlRequired":           "base URL is required",
	"form.baseUrlInvalid":            "base URL must be an http(s) URL",
	"form.modelRequired":             "model is required",
	"form.contextInvalid":            "context must be a non-negative integer",
	"approval.title":                 "Approval required",
	"approval.hint":                  "enter: approve   a: approve + always for this session   esc: deny",
	"note.notConnected":              "not connected",
	"note.betweenTurnsProvider":      "provider changes apply between turns",
	"note.betweenTurnsModel":         "model changes apply between turns",
	"note.savingModel":               "saving conversation model…",
	"note.betweenTurnsEffort":        "thinking effort changes apply between turns",
	"note.savingEffort":              "saving thinking effort…",
	"note.connected":                 "connected to {0}; session {1}",
	"chat.conversationModel":         "conversation model: {0}",
	"chat.usingEnvironment":          "using environment default",
	"chat.thinkingEffort":            "thinking effort: {0}",
	"chat.unknownCommand":            "unknown local command /{0}",
	"chat.mouseOn":                   "mouse on — wheel scrolls; plain drag selects and copies; click a tool card to expand it (ctrl+e cycles tool cards)",
	"chat.mouseOff":                  "mouse off — terminal-native drag selection; wheel is terminal-scroll (transcript: pgup/pgdn/ctrl+up)",
	"help.title":                     "local commands:",
	"help.new":                       "  /new [id]             start a new conversation",
	"help.session":                   "  /session [id]         open the conversation browser (or switch by id)",
	"help.provider":                  "  /provider [nickname|environment]  choose the global provider (e: edit, d: remove selected)",
	"help.model":                     "  /model [id|default]   choose this conversation's model",
	"help.connect":                   "  /connect              store a provider connection",
	"help.status":                    "  /status               show provider/model/context details",
	"help.mouse":                     "  /mouse [on|off]       wheel + drag selection (off = terminal-native mode)",
	"help.locale":                    "  /locale [en|zh|zh-TW] switch UI language",
	"help.help":                      "  /help                 show this help",
	"help.keys":                      "keys: ctrl+t thinking level (full → brief → off)  •  ctrl+e tool cards  •  ctrl+g thinking effort  •  tab completes /commands",
	"help.pluginTitle":               "plugin commands:",
	"chip.think":                     "think:{0}",
	"chip.tool":                      "tool:{0}",
	"chip.effort":                    "effort:{0}",
	"level.full":                     "full",
	"level.brief":                    "brief",
	"level.off":                      "off",
	"level.auto":                     "auto",
	"level.low":                      "low",
	"level.medium":                   "medium",
	"level.high":                     "high",
	"slash.completing":               "completing {0} …",
	"runtime.provider":               "provider?",
	"runtime.model":                  "model?",
	"runtime.env":                    " [env]",
	"runtime.global":                 " [global]",
	"runtime.session":                " [session]",
	"runtime.ctxNone":                "ctx —",
	"runtime.ctxEmpty":               "ctx {0} —/{1}",
	"runtime.ctxPct":                 "ctx {0} {1} {2}/{3}",
	"input.placeholder":              "message (alt+enter: newline)",
	"status.detailProvider":          "provider: {0} ({1})",
	"status.detailModel":             "model: {0}",
	"status.detailCatalog":           "catalog: {0}",
	"status.detailContext":           "context: {0} ({1})",
	"status.detailOutput":            "output: {0} ({1})",
	"status.detailUsed":              "used: {0} ({1})",
	"status.detailCache":             "cache hits: {0} of {1} prompt ({2})",
	"status.detailStrip":             "strip model prefix: on",
	"status.detailOverride":          "session model override: {0}",
	"status.unknown":                 "unknown",
	"status.unknownSource":           "unknown source",
	"status.none":                    "none",
	"locale.switched":                "UI language: {0}",
	"locale.invalid":                 "unknown locale {0} (use en, zh or zh-TW)",
}

var catalogZh = map[string]string{
	"status.ready":                   "就绪",
	"status.connecting":              "正在连接 {0}",
	"status.stopping":                "正在停止…",
	"status.stopArmed":               "停止？  （再按一次 esc 取消）",
	"status.working":                 "工作中",
	"status.updating":                "正在更新设置",
	"status.hint":                    "| /provider /model /connect /help | alt+enter: 换行 | ctrl+r: 历史 | ctrl+e: 工具 | ctrl+t: 思考 | ctrl+g: 深度 | esc: 停止 | ctrl+c: 退出",
	"footer.filterChoose":            "/: 过滤  •  enter: 选择  •  esc: 返回",
	"footer.filterSwitch":            "/: 过滤  •  enter: 切换  •  e: 编辑  •  d: 移除  •  esc: 返回",
	"footer.confirmRemove":           "移除 {0}？再按 d/x 确认，esc 取消",
	"search.noMatch":                 "（无匹配）",
	"search.prompt":                  "（反向搜索）`{0}`: {1}",
	"selector.providers":             "提供商 — 全局默认",
	"selector.connectCatalog":        "连接提供商 — 选择目录模板",
	"selector.sessionsLoading":       "会话 — 加载中…",
	"selector.sessions":              "会话",
	"selector.models":                "模型",
	"selector.envDefault":            "环境默认（NIF_OPENAI_*）",
	"selector.fallback":              "回退",
	"selector.connectProvider":       "+ 连接提供商",
	"selector.connectProviderDesc":   "存储 API key 与 OpenAI 兼容端点",
	"selector.customProvider":        "自定义 OpenAI 兼容端点",
	"selector.customProviderDesc":    "手动配置昵称、端点、目录与模型",
	"oauth.option.openai.browser":    "使用 ChatGPT 登录（Plus/Pro）",
	"oauth.option.openai.desc":       "订阅 OAuth 登录 — 浏览器授权",
	"oauth.option.openai.device":     "使用 ChatGPT 登录（设备码）",
	"oauth.option.openai.deviceDesc": "无界面订阅登录，通过设备码授权",
	"oauth.option.anthropic":         "使用 Claude 登录（Pro/Max）",
	"oauth.option.anthropic.desc":    "订阅 OAuth 登录 — 浏览器授权",
	"oauth.title":                    "登录 {0}",
	"oauth.meta":                     "订阅凭据由 Niffler 存储并自动刷新；令牌绝不进入聊天历史。",
	"oauth.openHint":                 "请在浏览器中打开：",
	"oauth.deviceCode":               "设备码：",
	"oauth.waiting":                  "等待授权中…",
	"oauth.completing":               "正在完成授权…",
	"oauth.callbackUnavailable":      "本地回调端口不可用 — 登录完成后，请在下方粘贴最终跳转 URL。",
	"oauth.manualPrompt":             "授权码",
	"oauth.manualPlaceholder":        "授权码或跳转 URL（可选）",
	"oauth.manualHint":               "enter: 提交授权码/URL",
	"oauth.esc":                      "esc: 取消登录",
	"oauth.keys":                     "esc: 取消  •  enter: 提交授权码/URL",
	"oauth.signedIn":                 "登录成功：{0} — 令牌已存储并自动刷新",
	"selector.catalog":               "目录 {0}",
	"selector.modelsCount":           "{0} 个模型",
	"selector.connected":             "已连接",
	"selector.providerDefault":       "提供商默认",
	"selector.useProviderDefault":    "使用提供商默认",
	"selector.reasoning":             "推理",
	"selector.tools":                 "工具",
	"selector.ctx":                   "ctx {0}",
	"selector.newSession":            "+ 新会话",
	"selector.newSessionDesc":        "开始新对话",
	"form.nickname":                  "昵称",
	"form.nicknameLocked":            "昵称（锁定）",
	"form.baseUrl":                   "Base URL",
	"form.catalogId":                 "目录 ID",
	"form.model":                     "模型",
	"form.context":                   "上下文",
	"form.apiKey":                    "API key",
	"form.connectTitle":              "连接提供商 — {0}",
	"form.editTitle":                 "编辑提供商 — {0}",
	"form.connectMeta":               "OpenAI 兼容端点；凭据由 Niffler 存储，绝不写入聊天历史。",
	"form.editMeta":                  "正在编辑 {0}；API key 留空则保留已存凭据。",
	"form.saving":                    "正在保存提供商…",
	"form.keys":                      "tab/shift+tab: 字段  •  enter: 下一步/保存  •  ctrl+s: 保存  •  esc: 取消",
	"form.apiKeyRequired":            "需要 API key（本地无密钥端点请填占位符）",
	"form.leaveBlankToKeep":          "留空则保留",
	"form.oauthLeaveBlank":           "OAuth 登录 — 留空保留令牌",
	"form.prompt.nickname":           "昵称   ",
	"form.prompt.baseUrl":            "Base URL   ",
	"form.prompt.apiKey":             "API key    ",
	"form.prompt.catalog":            "目录 ID ",
	"form.prompt.model":              "模型       ",
	"form.prompt.context":            "上下文     ",
	"form.placeholder.apiKey":        "必填",
	"form.placeholder.catalog":       "models.dev 提供商 ID（可选）",
	"form.placeholder.model":         "自动从目录选择；可手动覆盖",
	"form.placeholder.context":       "0 = 自动",
	"form.nicknameRequired":          "需要昵称",
	"form.baseUrlRequired":           "需要 Base URL",
	"form.baseUrlInvalid":            "Base URL 必须是 http(s) URL",
	"form.modelRequired":             "需要模型",
	"form.contextInvalid":            "上下文必须是非负整数",
	"approval.title":                 "需要批准",
	"approval.hint":                  "enter: 批准   a: 批准且本会话始终允许   esc: 拒绝",
	"note.notConnected":              "未连接",
	"note.betweenTurnsProvider":      "提供商变更在回合之间生效",
	"note.betweenTurnsModel":         "模型变更在回合之间生效",
	"note.savingModel":               "正在保存会话模型…",
	"note.betweenTurnsEffort":        "思考深度需在回合间切换",
	"note.savingEffort":              "正在保存思考深度…",
	"note.connected":                 "已连接 {0}；会话 {1}",
	"chat.conversationModel":         "会话模型：{0}",
	"chat.usingEnvironment":          "使用环境默认",
	"chat.thinkingEffort":            "思考深度：{0}",
	"chat.unknownCommand":            "未知本地命令 /{0}",
	"chat.mouseOn":                   "鼠标已开启 — 滚轮滚动；直接拖拽选择并复制；点击工具卡片展开（ctrl+e 循环工具卡片）",
	"chat.mouseOff":                  "鼠标已关闭 — 终端原生拖拽选择；滚轮为终端滚动（记录：pgup/pgdn/ctrl+up）",
	"help.title":                     "本地命令：",
	"help.new":                       "  /new [id]             开始新对话",
	"help.session":                   "  /session [id]         打开会话浏览器（或按 id 切换）",
	"help.provider":                  "  /provider [昵称|environment]  选择全局提供商（e: 编辑，d: 移除选中）",
	"help.model":                     "  /model [id|default]   选择本会话的模型",
	"help.connect":                   "  /connect              存储提供商连接",
	"help.status":                    "  /status               显示提供商/模型/上下文详情",
	"help.mouse":                     "  /mouse [on|off]       滚轮 + 拖拽选择（off = 终端原生模式）",
	"help.locale":                    "  /locale [en|zh|zh-TW] 切换界面语言",
	"help.help":                      "  /help                 显示本帮助",
	"help.keys":                      "按键：ctrl+t 思考显示（完整 → 简要 → 关闭） • ctrl+e 工具卡片 • ctrl+g 思考深度 • tab 补全 /命令",
	"help.pluginTitle":               "插件命令：",
	"chip.think":                     "思考:{0}",
	"chip.tool":                      "工具:{0}",
	"chip.effort":                    "深度:{0}",
	"level.full":                     "完整",
	"level.brief":                    "简要",
	"level.off":                      "关闭",
	"level.auto":                     "自动",
	"level.low":                      "低",
	"level.medium":                   "中",
	"level.high":                     "高",
	"slash.completing":               "补全 {0} …",
	"runtime.provider":               "提供商？",
	"runtime.model":                  "模型？",
	"runtime.env":                    " [env]",
	"runtime.global":                 " [全局]",
	"runtime.session":                " [会话]",
	"runtime.ctxNone":                "ctx —",
	"runtime.ctxEmpty":               "ctx {0} —/{1}",
	"runtime.ctxPct":                 "ctx {0} {1} {2}/{3}",
	"input.placeholder":              "消息（alt+enter: 换行）",
	"status.detailProvider":          "提供商：{0}（{1}）",
	"status.detailModel":             "模型：{0}",
	"status.detailCatalog":           "目录：{0}",
	"status.detailContext":           "上下文：{0}（{1}）",
	"status.detailOutput":            "输出：{0}（{1}）",
	"status.detailUsed":              "已用：{0}（{1}）",
	"status.detailCache":             "缓存命中：{0} / {1} 提示词（{2}）",
	"status.detailStrip":             "剥离模型前缀：开",
	"status.detailOverride":          "会话模型覆盖：{0}",
	"status.unknown":                 "未知",
	"status.unknownSource":           "未知来源",
	"status.none":                    "无",
	"locale.switched":                "界面语言：{0}",
	"locale.invalid":                 "未知语言 {0}（用 en、zh 或 zh-TW）",
}

var catalogZhTW = map[string]string{
	"status.ready":                   "就緒",
	"status.connecting":              "正在連線 {0}",
	"status.stopping":                "正在停止…",
	"status.stopArmed":               "停止？  （再按一次 esc 取消）",
	"status.working":                 "工作中",
	"status.updating":                "正在更新設定",
	"status.hint":                    "| /provider /model /connect /help | alt+enter: 換行 | ctrl+r: 歷史 | ctrl+e: 工具 | ctrl+t: 思考 | ctrl+g: 深度 | esc: 停止 | ctrl+c: 結束",
	"footer.filterChoose":            "/: 過濾  •  enter: 選擇  •  esc: 返回",
	"footer.filterSwitch":            "/: 過濾  •  enter: 切換  •  e: 編輯  •  d: 移除  •  esc: 返回",
	"footer.confirmRemove":           "移除 {0}？再按 d/x 確認，esc 取消",
	"search.noMatch":                 "（無相符）",
	"search.prompt":                  "（反向搜尋）`{0}`: {1}",
	"selector.providers":             "供應商 — 全域預設",
	"selector.connectCatalog":        "連線供應商 — 選擇目錄範本",
	"selector.sessionsLoading":       "對話 — 載入中…",
	"selector.sessions":              "對話",
	"selector.models":                "模型",
	"selector.envDefault":            "環境預設（NIF_OPENAI_*）",
	"selector.fallback":              "回退",
	"selector.connectProvider":       "+ 連線供應商",
	"selector.connectProviderDesc":   "儲存 API key 與 OpenAI 相容端點",
	"selector.customProvider":        "自訂 OpenAI 相容端點",
	"selector.customProviderDesc":    "手動設定暱稱、端點、目錄與模型",
	"oauth.option.openai.browser":    "使用 ChatGPT 登入（Plus/Pro）",
	"oauth.option.openai.desc":       "訂閱 OAuth 登入 — 瀏覽器授權",
	"oauth.option.openai.device":     "使用 ChatGPT 登入（裝置碼）",
	"oauth.option.openai.deviceDesc": "無介面訂閱登入，透過裝置碼授權",
	"oauth.option.anthropic":         "使用 Claude 登入（Pro/Max）",
	"oauth.option.anthropic.desc":    "訂閱 OAuth 登入 — 瀏覽器授權",
	"oauth.title":                    "登入 {0}",
	"oauth.meta":                     "訂閱憑證由 Niffler 儲存並自動重新整理；權杖絕不進入對話歷史。",
	"oauth.openHint":                 "請在瀏覽器中開啟：",
	"oauth.deviceCode":               "裝置碼：",
	"oauth.waiting":                  "等待授權中…",
	"oauth.completing":               "正在完成授權…",
	"oauth.callbackUnavailable":      "本機回呼連接埠不可用 — 登入完成後，請在下方貼上最終重新導向 URL。",
	"oauth.manualPrompt":             "授權碼",
	"oauth.manualPlaceholder":        "授權碼或重新導向 URL（可選）",
	"oauth.manualHint":               "enter: 送出授權碼/URL",
	"oauth.esc":                      "esc: 取消登入",
	"oauth.keys":                     "esc: 取消  •  enter: 送出授權碼/URL",
	"oauth.signedIn":                 "登入成功：{0} — 權杖已儲存並自動重新整理",
	"selector.catalog":               "目錄 {0}",
	"selector.modelsCount":           "{0} 個模型",
	"selector.connected":             "已連線",
	"selector.providerDefault":       "供應商預設",
	"selector.useProviderDefault":    "使用供應商預設",
	"selector.reasoning":             "推理",
	"selector.tools":                 "工具",
	"selector.ctx":                   "ctx {0}",
	"selector.newSession":            "+ 新對話",
	"selector.newSessionDesc":        "開始新對話",
	"form.nickname":                  "暱稱",
	"form.nicknameLocked":            "暱稱（鎖定）",
	"form.baseUrl":                   "Base URL",
	"form.catalogId":                 "目錄 ID",
	"form.model":                     "模型",
	"form.context":                   "上下文",
	"form.apiKey":                    "API key",
	"form.connectTitle":              "連線供應商 — {0}",
	"form.editTitle":                 "編輯供應商 — {0}",
	"form.connectMeta":               "OpenAI 相容端點；憑證由 Niffler 儲存，絕不寫入聊天歷史。",
	"form.editMeta":                  "正在編輯 {0}；API key 留空則保留已存憑證。",
	"form.saving":                    "正在儲存供應商…",
	"form.keys":                      "tab/shift+tab: 欄位  •  enter: 下一步/儲存  •  ctrl+s: 儲存  •  esc: 取消",
	"form.apiKeyRequired":            "需要 API key（本地無金鑰端點請填佔位符）",
	"form.leaveBlankToKeep":          "留空則保留",
	"form.oauthLeaveBlank":           "OAuth 登入 — 留空保留權杖",
	"form.prompt.nickname":           "暱稱   ",
	"form.prompt.baseUrl":            "Base URL   ",
	"form.prompt.apiKey":             "API key    ",
	"form.prompt.catalog":            "目錄 ID ",
	"form.prompt.model":              "模型       ",
	"form.prompt.context":            "上下文     ",
	"form.placeholder.apiKey":        "必填",
	"form.placeholder.catalog":       "models.dev 供應商 ID（可選）",
	"form.placeholder.model":         "自動從目錄選擇；可手動覆寫",
	"form.placeholder.context":       "0 = 自動",
	"form.nicknameRequired":          "需要暱稱",
	"form.baseUrlRequired":           "需要 Base URL",
	"form.baseUrlInvalid":            "Base URL 必須是 http(s) URL",
	"form.modelRequired":             "需要模型",
	"form.contextInvalid":            "上下文必須是非負整數",
	"approval.title":                 "需要核准",
	"approval.hint":                  "enter: 核准   a: 核准且本對話一律允許   esc: 拒絕",
	"note.notConnected":              "未連線",
	"note.betweenTurnsProvider":      "供應商變更在回合之間生效",
	"note.betweenTurnsModel":         "模型變更在回合之間生效",
	"note.savingModel":               "正在儲存對話模型…",
	"note.betweenTurnsEffort":        "思考深度需在回合間切換",
	"note.savingEffort":              "正在儲存思考深度…",
	"note.connected":                 "已連線 {0}；對話 {1}",
	"chat.conversationModel":         "對話模型：{0}",
	"chat.usingEnvironment":          "使用環境預設",
	"chat.thinkingEffort":            "思考深度：{0}",
	"chat.unknownCommand":            "未知的本機指令 /{0}",
	"chat.mouseOn":                   "滑鼠已開啟 — 滾輪捲動；直接拖曳選取並複製；點擊工具卡片展開（ctrl+e 循環工具卡片）",
	"chat.mouseOff":                  "滑鼠已關閉 — 終端機原生拖曳選取；滾輪為終端機捲動（記錄：pgup/pgdn/ctrl+up）",
	"help.title":                     "本機指令：",
	"help.new":                       "  /new [id]             開始新對話",
	"help.session":                   "  /session [id]         開啟會話瀏覽器（或按 id 切換）",
	"help.provider":                  "  /provider [暱稱|environment]  選擇全域供應商（e: 編輯，d: 移除選取）",
	"help.model":                     "  /model [id|default]   選擇本對話的模型",
	"help.connect":                   "  /connect              儲存供應商連線",
	"help.status":                    "  /status               顯示供應商/模型/上下文詳情",
	"help.mouse":                     "  /mouse [on|off]       滾輪 + 拖曳選取（off = 終端機原生模式）",
	"help.locale":                    "  /locale [en|zh|zh-TW] 切換介面語言",
	"help.help":                      "  /help                 顯示本說明",
	"help.keys":                      "按鍵：ctrl+t 思考顯示（完整 → 簡要 → 關閉） • ctrl+e 工具卡片 • ctrl+g 思考深度 • tab 補全 /指令",
	"help.pluginTitle":               "插件指令：",
	"chip.think":                     "思考:{0}",
	"chip.tool":                      "工具:{0}",
	"chip.effort":                    "深度:{0}",
	"level.full":                     "完整",
	"level.brief":                    "簡要",
	"level.off":                      "關閉",
	"level.auto":                     "自動",
	"level.low":                      "低",
	"level.medium":                   "中",
	"level.high":                     "高",
	"slash.completing":               "補全 {0} …",
	"runtime.provider":               "供應商？",
	"runtime.model":                  "模型？",
	"runtime.env":                    " [env]",
	"runtime.global":                 " [全域]",
	"runtime.session":                " [對話]",
	"runtime.ctxNone":                "ctx —",
	"runtime.ctxEmpty":               "ctx {0} —/{1}",
	"runtime.ctxPct":                 "ctx {0} {1} {2}/{3}",
	"input.placeholder":              "訊息（alt+enter: 換行）",
	"status.detailProvider":          "供應商：{0}（{1}）",
	"status.detailModel":             "模型：{0}",
	"status.detailCatalog":           "目錄：{0}",
	"status.detailContext":           "上下文：{0}（{1}）",
	"status.detailOutput":            "輸出：{0}（{1}）",
	"status.detailUsed":              "已用：{0}（{1}）",
	"status.detailCache":             "快取命中：{0} / {1} 提示詞（{2}）",
	"status.detailStrip":             "剝離模型前綴：開",
	"status.detailOverride":          "對話模型覆寫：{0}",
	"status.unknown":                 "未知",
	"status.unknownSource":           "未知來源",
	"status.none":                    "無",
	"locale.switched":                "介面語言：{0}",
	"locale.invalid":                 "未知語言 {0}（用 en、zh 或 zh-TW）",
}

var catalogs = map[Locale]map[string]string{
	LocaleEN:   catalogEn,
	LocaleZH:   catalogZh,
	LocaleZHTW: catalogZhTW,
}

// t translates key for loc, substituting {0} {1} … with vars. Unknown keys
// fall back to the English catalog so a stale translation never blanks the
// screen.
func t(loc Locale, key string, vars ...string) string {
	s, ok := catalogs[loc][key]
	if !ok {
		s = catalogEn[key]
	}
	for i, v := range vars {
		s = strings.Replace(s, fmt.Sprintf("{%d}", i), v, 1)
	}
	return s
}

func validLocale(s string) (Locale, bool) {
	switch Locale(s) {
	case LocaleEN, LocaleZH, LocaleZHTW:
		return Locale(s), true
	}
	return "", false
}

// localeFilePath is the single-line persistence file for the /locale
// choice (same state dir as history, empty when unavailable).
func localeFilePath() string {
	dir := strings.TrimSpace(os.Getenv("XDG_STATE_HOME"))
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "niffler-tui", "locale")
}

// detectLocale resolves the startup locale: an explicit /locale choice,
// then NIF_TUI_LOCALE, then LANG/LC_ALL, then English.
func detectLocale() Locale {
	if path := localeFilePath(); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			if loc, ok := validLocale(strings.TrimSpace(string(data))); ok {
				return loc
			}
		}
	}
	if env := strings.TrimSpace(os.Getenv("NIF_TUI_LOCALE")); env != "" {
		if loc, ok := validLocale(env); ok {
			return loc
		}
	}
	lang := strings.TrimSpace(os.Getenv("LC_ALL"))
	if lang == "" {
		lang = strings.TrimSpace(os.Getenv("LANG"))
	}
	lower := strings.ToLower(lang)
	if strings.HasPrefix(lower, "zh") {
		if strings.Contains(lower, "tw") || strings.Contains(lower, "hk") ||
			strings.Contains(lower, "hant") {
			return LocaleZHTW
		}
		return LocaleZH
	}
	return LocaleEN
}

// persistLocale stores the /locale choice (best effort; the in-memory
// value still applies for this run when the file is unavailable).
func persistLocale(loc Locale) {
	if path := localeFilePath(); path != "" {
		if dir := filepath.Dir(path); dir != "" {
			_ = os.MkdirAll(dir, 0o755)
		}
		_ = os.WriteFile(path, []byte(string(loc)+"\n"), 0o644)
	}
}
