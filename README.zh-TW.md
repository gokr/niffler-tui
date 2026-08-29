# niffler-tui

[English](README.md) · [简体中文](README.zh.md) · 繁體中文

Niffler 的 Bubble Tea 終端機客戶端。和桌面 UI 一樣，它是個匯流排客戶端：
經 `svc.core.call` 驅動 `session` 呼叫，並渲染 `ev.session.*` 的 token、
tool-call、assistant 與 completion 事件。

該組件以零工具註冊。執行它不會給任何 LLM 的工具上下文新增東西。

介面語言（en/zh/zh-TW）依序由 `/locale` 的持久化選擇、`NIF_TUI_LOCALE`、
`LANG`/`LC_ALL`（zh_TW/zh_HK → 繁體，其他 zh → 簡體）決定，預設英文；
執行期可用 `/locale [en|zh|zh-TW]` 即時切換。

## 建置

期望把本倉庫 checkout 在 Niffler 旁邊，這樣本地 `go.mod` 的 replacement
能找到 Go SDK：

```bash
make            # 建置 bin/niffler-tui
make test       # 跑 TUI 測試套件
make run        # 建置 + 執行
```

或直接：

```bash
go build -o bin/niffler-tui ./tui
```

Niffler 的 builder 不使用這個 `go.mod`；它建立隔離的模組，replacement
指向它自己的 `sdk/go` 目錄。

## 執行

啟動 Niffler，然後從真實終端機執行客戶端：

```bash
./bin/niffler-tui
./bin/niffler-tui -session game
```

設定：

- `-session <id>` 選擇對話，預設依序取 `NIF_SESSION`、`console`。
- `NIF_NATS_URL` 選擇匯流排。未設定時，客戶端依序檢查
  `$NIF_ROOT/var/nats-url`、`./var/nats-url`，最後是
  `nats://127.0.0.1:4222`。

按鍵：

- `Enter`：送出目前輸入
- `Alt+Enter` / `Ctrl+J`：在輸入裡插入換行（多行訊息）
- `Up` / `Down`：在輸入內移動；在首行/末行時按 readline 方式瀏覽已送出
  訊息歷史，復原進行中的草稿
- `Ctrl+R`：反向歷史搜尋；鍵入過濾，再按 `Ctrl+R` 看更早的相符項，
  `Enter` 接受，`Esc` 取消
- `PgUp` / `PgDn`：翻頁瀏覽紀錄
- `Ctrl+Up` / `Ctrl+Down`：逐行捲動紀錄
- `Ctrl+T`：展開/收起所有 tool-run 卡片
- 滑鼠滾輪：捲動紀錄
- `Ctrl+C`：結束

同一回合裡連續的工具呼叫摺疊成一張可收起的「tool-run card」，帶摘要行
（✓/⚠ 符號、呼叫次數、名稱），預設收起，長工具序列不礙眼。`Ctrl+T`
展開或收起每張卡片。滑鼠追蹤**預設開啟**：滾輪捲動紀錄，左鍵點擊切換
游標下的卡片。因為追蹤會擷取點擊，複製/貼上選擇用 `Shift`+拖曳（SGR
滑鼠的標準方式）。`/mouse off` 完全關閉追蹤——原生拖曳複製復原，但應用
收不到滾輪事件，紀錄改用 `PgUp`/`PgDn`/`Ctrl+Up` 捲動，滾輪退化為普通
終端機捲動。

本地指令由 TUI 處理，絕不發給模型：

- `/provider` —— 可搜尋的全域供應商選擇器；也提供環境回退與供應商設定
- `/model` —— 可搜尋的目前供應商模型目錄；選中立即持久化為本對話的
  覆寫（無需推理呼叫）
- `/connect` —— 遮罩的供應商連線表單，使用 models.dev 範本或自訂
  OpenAI 相容端點
- `/status` —— 詳細的有效供應商/模型/上下文來源與用量
- `/mouse [on|off]` —— 滑鼠追蹤（預設開：滾輪+點擊可用，複製用
  Shift+拖曳；關閉後復原本生拖曳複製，但應用收不到滾輪）
- `/locale [en|zh|zh-TW]` —— 切換介面語言（持久化到使用者狀態目錄）
- `/help` —— 指令摘要

選擇器用 `/` 過濾、方向鍵移動、`Enter` 選中、`Esc` 返回聊天。供應商
表單會遮罩 API key 欄位；斜線指令和憑證不會寫入已送出訊息歷史或紀錄。

第二行標頭始終顯示有效的供應商與模型，外加內容占用條。內容占用
在可用時使用模型上報的總 token 數，經 Niffler 的對話元資料跨對話復原
存續，並在與 core 相同的 75% 警告 / 90% 裁剪閾值處變色。供應商選擇
改變 Niffler 的全域預設；模型選擇僅限目前對話。

Assistant 回覆渲染為 Markdown（標題、粗體、斜體、列表、表格，以及經
Glamour 語法高亮的程式碼塊）。token 串流期間，塊以純文字顯示，輸出暫停
後升級為帶樣式的 Markdown，避免每個 token 都做一次昂貴的完整 Markdown
渲染。

歷史按對話以 JSONL 持久化在
`$XDG_STATE_HOME/niffler-tui/history-<session>.jsonl`（或
`~/.local/state/niffler-tui/`），上限 200 條。刪除該檔案即清空。

Markdown 樣式跟隨 `GLAMOUR_STYLE` 環境變數（預設 `dark`；見 Glamour
的樣式，如 `light`、`ascii`、`dracula`、`tokyo-night`）。

備用螢幕讓視埠與輸入在 token 串流期間保持穩定。紀錄向上捲動後，新的
token 不會強制把它拉回底部。

## 外掛狀態

`niffler.json` 把該組件標記為 `"interactive": true`：它應由外掛安裝
建置，但由使用者在自己的終端機裡手動啟動：

```bash
./var/bin/cli install gokr/niffler-tui
./var/bin/tui -session game
```

互動式組件不受 Niffler 監督或重啟。更新或移除該套件之前請結束正在執行
的 TUI。

## 社群

[Discord](https://discord.gg/ThJFEAJUAk) · 授權：MIT，見
[LICENSE](LICENSE)。
