# niffler-tui

[English](README.md) · [简体中文](README.zh.md) · 繁體中文 · [Discord](https://discord.gg/ThJFEAJUAk)

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

- `Enter`：送出目前輸入；回合執行中按 Enter 則「轉向」（steer）——該訊息
  以 “Steer: …” 行摺入正在執行的回合
- `Alt+Enter` / `Ctrl+J`：在輸入裡插入換行（多行訊息）
- `Up` / `Down`：在輸入內移動；在首行/末行時按 readline 方式瀏覽已送出
  訊息歷史，復原進行中的草稿
- `Ctrl+R`：反向歷史搜尋；鍵入過濾，再按 `Ctrl+R` 看更早的相符項，
  `Enter` 接受，`Esc` 取消
- `Tab` / `Shift+Tab`：補全游標下的斜線指令或參數——輸入上方出現一條
  暗色候選行，`Tab`/`Shift+Tab` 循環高亮候選，`Esc`（或任意其他鍵）
  取消，僅有一個候選時直接填入
- `PgUp` / `PgDn`：翻頁瀏覽紀錄
- `Ctrl+Up` / `Ctrl+Down`：逐行捲動紀錄
- `Ctrl+T`：循環思考可見性（`full` → `brief` → `off`）
- `Ctrl+E`：循環 tool-run 卡片可見性（`brief` → `full` → `off`）
- `Ctrl+G`：輪換本對話的 LLM 思考力度（`auto` → `low` → `medium` →
  `high`）
- `Esc`：回合執行中，第一次按下進入 “Stop?” 待命，第二次強制取消目前
  回合
- 滑鼠：滾輪捲動紀錄；直接拖曳選取並複製；左鍵點擊切換 tool-run 卡片
- `Ctrl+C`：結束

同一回合裡連續的工具呼叫摺疊成一張可收起的「tool-run card」，帶摘要行
（✓/⚠ 符號、呼叫次數、名稱），預設收起，長工具序列不礙眼。`Ctrl+E`
在 brief（收起）、full（全部展開）、off（隱藏）之間循環；左鍵點擊切換
游標下的卡片。滑鼠追蹤**預設開啟**：滾輪捲動紀錄，直接按住左鍵拖曳會
由應用選取並複製文字，兩者可同時使用。`/mouse off` 仍可切換到終端機
原生選取，但應用會收不到滾輪事件，紀錄改用
`PgUp`/`PgDn`/`Ctrl+Up` 捲動，滾輪退化為普通終端機捲動。

### 思考

模型推理以灰色斜體顯示在每條 assistant 回覆上方，按回合放置在紀錄中。
`Ctrl+T` 循環顯示量：`full`（全部）、`brief`（每個區塊一行暗色
`▸ thinking…`）、`off`（完全隱藏）。推理在顯示時被壓縮：修剪邊緣換行，
內部空行串摺疊為單個換行，段落緊湊排列，不再堆成一片空白。

`Ctrl+G` 輪換本對話的 LLM 思考*力度*——`auto`（供應商預設）→ `low` →
`medium` → `high`。該選擇與模型覆寫一樣按對話持久化，在回合之間生效，
僅在設定時以 `reasoning_effort` 轉發給 LLM，因此不支援思考力度的供應商
永遠不會見到它。目前選擇顯示在標頭的 `effort:auto|low|medium|high` 中。

本地指令由 TUI 處理，絕不發給模型：

- `/provider` —— 可搜尋的全域供應商選擇器（e：編輯，d：移除）；也提供
  環境回退、供應商設定與訂閱登入（ChatGPT Plus/Pro 支援瀏覽器或裝置碼，
  Claude Pro/Max 支援瀏覽器）。OAuth 流程會開啟授權 URL，輪詢直到
  Niffler 的 provider 元件存入權杖，本機回呼連接埠不可用時可貼上
  授權碼/重新導向 URL；esc 取消
- `/model` —— 可搜尋的目前供應商模型目錄；選中立即持久化為本對話的
  覆寫（無需推理呼叫）
- `/connect` —— 遮罩的供應商連線表單，使用 models.dev 範本或自訂
  OpenAI 相容端點
- `/status` —— 詳細的有效供應商/模型/上下文來源與用量
- `/new [id]` —— 開始一個新對話
- `/session` —— 對話瀏覽器；切換或復原對話，或開始新對話
- `/locale [en|zh|zh-TW]` —— 切換介面語言（持久化到使用者狀態目錄）
- `/mouse [on|off]` —— 滑鼠追蹤（預設開：滾輪、點擊和直接拖曳選取可
  同時使用；關閉後使用終端機原生選取，但應用收不到滾輪）
- `/help` —— 指令摘要，含已註冊的外掛指令

Tab 補全適用於指令名，以及指令宣告的參數值（內聯候選，或經宣告的來源
工具惰性取得）。未知指令會提示相近的指令名。

選擇器用 `/` 過濾、方向鍵移動、`Enter` 選中、`Esc` 返回聊天。供應商
表單會遮罩 API key 欄位；斜線指令和憑證不會寫入已送出訊息歷史或紀錄。

標頭顯示對話 id、`think:`/`tool:`/`effort:` 狀態區塊，以及有效的供應商
與模型，外加內容占用條。內容佔用在可用時使用模型上報的總 token 數，
經 Niffler 的對話元資料跨對話復原存續，並在與 core 相同的 75% 警告 /
90% 裁剪閾值處變色。供應商選擇改變 Niffler 的全域預設；模型選擇僅限
目前對話。

## 外掛斜線指令

組件可以宣告式擴充 TUI，無需任何 UI 程式碼：組件照常註冊工具，並在
註冊資訊中新增 `slash` 段（`{name, description, tool, params[]}`，參數
可帶補全來源；見 Niffler 倉庫的 `docs/WIRE.md`）。core 校驗該規格，把
合併後的指令表寫入 store 檢查點，並在每次變更時廣播
`ev.catalog.updated`；TUI 先讀 store，再即時跟隨該事件。

執行已註冊指令時，TUI 按宣告的參數解析指令行（位置參數、`name=value`、
裸布林旗標、預設值）並呼叫目標工具；結果顯示為紀錄中的 meta 區塊。內建
指令遮蔽同名註冊，指令隨其組件退出而自動消失。

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
