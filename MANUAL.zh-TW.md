# niffler-tui Manual

[English](MANUAL.md) · [简体中文](MANUAL.zh.md) · [繁體中文](MANUAL.zh-TW.md) · [返回 README](README.zh-TW.md) · [Niffler harness](https://github.com/gokr/niffler)

> 🤖 AI 自動翻譯；如有歧義，以 [English](MANUAL.md) 為準。章節標題保留英文，以保持跨語言連結一致。

詳細使用說明與參考。安裝和首次對話請從 [README](README.zh-TW.md) 開始。遠端/fleet 支援可選，且屬於實驗功能。

## Contents

- [Run](#run) — 連線與會話選擇
- [Thinking](#thinking)
- [Command reference](#command-reference)
- [Shell escapes](#shell-escapes)
- [Plugin slash commands](#plugin-slash-commands)
- [Transcript and client state](#transcript-and-client-state)
- [Plugin status](#plugin-status)
- [Build](#build)
- [Fleet startup (experimental)](#fleet-startup-experimental)

## Run

先啟動 Niffler，再從真實終端執行客戶端：

```bash
./bin/niffler-tui
./bin/niffler-tui -session game
```

配置：

- `-session <id>` 選擇會話；預設依次使用 `NIF_SESSION`、該框架/工作區上次儲存的會話。如無已儲存選擇，則建立新會話。
- `NIF_NATS_URL` 選擇匯流排。未設定時，依次檢查 `$NIF_ROOT/var/nats-url`、`./var/nats-url`，最後使用 `nats://127.0.0.1:4222`。
- 介面語言依次取自 `/locale` 儲存的選擇、`NIF_TUI_LOCALE`（`en`/`zh`/`zh-TW`）、`LANG`/`LC_ALL`（zh_TW/zh_HK 為繁體，其他 zh 為簡體），最後為英語。

按鍵：

- `Enter`：傳送輸入；輪次正在執行時改為引導當前輪次，訊息以 “Steer: …” 行併入正在執行的輪次。
- `Alt+Enter` / `Ctrl+J`：插入換行，支援多行輸入。
- `Up` / `Down`：在輸入內移動；到達首行/末行時按 readline 風格瀏覽已傳送歷史，並恢復尚未傳送的草稿。
- `Ctrl+R`：反向搜尋歷史；輸入過濾，再按 `Ctrl+R` 查詢更早匹配，`Enter` 接受，`Esc` 取消。
- `Tab` / `Shift+Tab`：補全游標處的斜槓命令或引數。候選行顯示在輸入上方；按鍵迴圈選擇，`Esc` 或其他按鍵關閉，僅有一個候選時直接填入。
- `PgUp` / `PgDn`：翻頁滾動記錄。
- `Ctrl+Up` / `Ctrl+Down`：逐行滾動記錄。
- `Ctrl+T`：切換推理可見程度（`full` → `brief` → `off`）。
- `Ctrl+E`：切換工具卡片詳細程度（`brief` → `medium` → `full` → `off`）。
- `Ctrl+O`：開啟工作單元輪詢檢視。上方列出後臺程序和子代理，顯示狀態與執行時間；下方顯示後臺程序 stdout 尾部，或子代理記錄尾部（回覆、工具呼叫及受限長度的結果預覽），開啟時實時重新整理。`Ctrl+O`/`Tab`/`n` 切換工作單元，`r` 重新讀取，`Esc` 返回。沒有執行任務時提示 “nothing is running (bg/agent)”。
- `Shift+Tab`：輪換當前會話的 LLM 推理力度（`auto` → `low` → `medium` → `high` → `max`）。
- `Esc`：忙碌時首次按下顯示 “Stop?”，再次按下強制取消當前輪次。
- 滑鼠：滾輪滾動記錄；普通拖動選擇並複製；左鍵切換工具呼叫卡片；ctrl+單擊開啟指標下的 URL 或路徑；alt+拖動還會將選區儲存為片段。
- `Ctrl+X`：輪換收藏的提供商/模型組合（由 `/fav` 管理；與 `/model` 一樣，在輪次之間生效）。
- `Ctrl+C`：退出。

同一輪次連續的工具呼叫會合併為一張可摺疊的工具卡片。預設 `medium` 模式按工具型別展示：bash 顯示 `$ command`、輸出尾部、`... (N earlier lines, ctrl+e to expand)` 提示、退出狀態及耗時；edit 用紅/綠差異顯示修改前後文本和 `+A -R` 行數；read/write 顯示檔案開頭和摺疊提示；其他工具顯示名稱、引數和結果開頭。

`brief` 將卡片壓縮為一行摘要（✓/⚠、呼叫次數和名稱）；`full` 展開所有呼叫且不折疊輸出；`off` 隱藏卡片。`Ctrl+E` 切換級別，左鍵單擊則僅切換該卡片（brief→medium→full）。

**預設開啟滑鼠跟蹤**：滾輪滾動和應用內左鍵拖動選擇可以同時使用。鬆開滑鼠即複製，無需再按 `Cmd+C`。文字透過 OSC 52 傳送；本地執行（非 SSH 會話）時還透過平臺剪貼簿工具（`pbcopy`、`wl-copy`、`xclip` 或 `xsel`）複製，因此不支援 OSC 52 的 Apple Terminal、GNOME Terminal 也能寫入剪貼簿。

**Ctrl+單擊**在瀏覽器中開啟 URL，或透過系統預設程式開啟檔案。grep 風格的 `:line` 字尾會被移除；路徑相對於會話工作區、啟動目錄及框架根目錄解析。`NIF_TUI_OPEN` 可替換整個開啟命令，例如 `NIF_TUI_OPEN='kitty nvim'`。

**Alt+拖動**除複製外，還儲存片段。片段存入 store（kind `snippet`），帶所屬會話和時間戳；`/snippets` 可列出，模型也能透過 store 工具讀取。上述手勢要求終端能報告滑鼠修飾鍵（SGR 滑鼠編碼，即支援拖動選擇的 xterm 相容終端）。

`/mouse off` 可恢復終端原生選擇，但應用不再接收滾輪事件；此時使用 `PgUp`/`PgDn`/`Ctrl+Up` 滾動記錄，滾輪僅滾動終端本身。

### Thinking

模型推理顯示在每次助手回覆上方，按輪次排列。推理正文采用暗色斜體 Markdown，程式碼塊保留語法高亮。`Ctrl+T` 在 `full`（全部）、`brief`（每塊一行暗色 `▸ thinking…`）和 `off`（隱藏）間切換。展示時會刪除首尾換行，並將連續空行壓縮為一個，避免大塊空白。

`Shift+Tab` 調整推理**力度**：`auto`（提供商預設）→ `low` → `medium` → `high` → `max`。選擇與模型覆蓋一樣按會話儲存，在輪次之間生效；只有明確設定後才以 `reasoning_effort` 傳送，不支援此功能的提供商不會收到該欄位。

不同提供商的級別並非統一尺度。例如 DeepSeek 將 `minimal`/`low` 對映為 `low`，`medium`/`xhigh` 對映為 `high`，`max` 對映為 `max`；提供商支援時，`max` 是唯一高於 `high` 的級別。標題欄顯示 `effort:auto|low|medium|high|max`。

### Command reference

本地命令由 TUI 處理，不傳送給模型：

- `/provider` — 可搜尋的全域性提供商選擇器（e 編輯、d 刪除），提供環境回退、連線設定和訂閱登入（ChatGPT Plus/Pro 瀏覽器或裝置碼；Claude Pro/Max 瀏覽器）。OAuth 開啟授權 URL 並輪詢，直到 provider 元件儲存令牌；本地回撥埠不可用時可貼上程式碼/重定向 URL，Esc 取消。
- `/model` — 搜尋所有已配置提供商的模型。直接輸入即可按暱稱或模型 ID 過濾，無需先按 `/`；跨提供商選擇模型時，將提供商和模型一起固定到當前會話，不觸發推理呼叫。
- `/connect` — 使用 models.dev 模板或自定義 OpenAI 相容端點填寫遮蔽憑據的連線表單。
- `/mcp [add|edit|on|off|refresh|search]` — 管理外部 MCP 服務；不帶引數開啟服務瀏覽器。
- `/lsp [add|edit|remove]` — 管理語言伺服器註冊項。透過配置而非程式碼註冊（如 gopls 對應 .go），使 lsp 工具獲得診斷和精確導航；不帶引數開啟瀏覽器。
- `/config [domain]` —— 瀏覽並編輯元件宣告的設定：只輸入 `/config` 列出所有配置域
  （擁有者、設定項數量、變更何時生效），`/config repomap.gates` 開啟該域的設定項。
  選擇一項、輸入新值，Enter 即透過 harness 的 `config_patch` 提交，並附帶你當時看到的
  修訂號，因此並行修改會顯示為衝突而不是遺失更新。金鑰顯示為 `set`/`unset`，客戶端
  從不持有其值；唯讀項會被標記；擁有者未執行的配置域會列出原因，而不是在提交時才失敗。
  表單由各宣告的 schema 產生，新擁有者無需修改客戶端。
- `/processes` — 管理由 bash 的 run_in_background 或 process_start 啟動的開發服務、監視器等後臺程序。執行時狀態列顯示 `bg N`；Enter 預覽輸出尾部但不改變模型的讀取遊標，d 經兩步確認後終止程序。
- `/tasks`（`/inbox`）— 艦隊收件匣：你提交給遠端 Niffler 的任務，及其生命週期狀態與（結算後的）有界結論。這是一次本機讀取，因此即使執行個體已離線或不存在，任務仍會顯示它曾報告的內容；詳細內容由 `fleet_task_result` 取得，需要對方在線。連線期間到達的結算只在狀態列提示，不會搶佔檢視區域。未讀標記只屬於你，保存在用戶端自身狀態中，絕不會傳送給 harness，因此不會確認模型的任何通知。首次連線會把既有歷史作為基準，而不是全部顯示為新訊息。
- `/status` — 顯示實際提供商、模型、上下文來源和用量。
- `/info [id]` — 從 store 訊息計算會話統計：各角色訊息數、各工具呼叫數、結果大小（總和、中位數、最大值及帶預覽的大結果）、累計 token 和 discover/invoke 比例。預設當前會話；可用補全選擇其他 ID，子代理顯示父會話。
- `/name [name]` — 設定會話名稱（標題欄標題，替換由首條訊息擷取的名稱）；不帶引數顯示已儲存名稱。
- `/copy` — 將最後一次助手回覆複製到剪貼簿，使用與拖動選擇相同的複製機制。
- `/fav [add|rm <n>|<n>]` — 管理收藏的提供商/模型組合，儲存為有序 store 文件（kind `modelfavorite`）。`<n>` 應用條目，`Ctrl+X` 按順序迴圈，在輪次之間生效。
- `/doctor [deep]` — 顯示 Markdown 健康檢查表；`deep` 執行實時探測。
- `/export [path]` — 將當前實際提供商請求匯出為格式化 JSON。不帶路徑時顯示在記錄中（有長度限制）；帶路徑時相對於會話工作區寫檔案。
- `/compact` — 立即執行可替換的壓縮元件，不執行 LLM 會話輪次。舊歷史被驗證過的檢查點替換；長會話可在自動壓力處理前主動壓縮。元件拒絕時會明確報告，不靜默退化為有損裁剪。
- `/approvals [ask|auto]` — 控制當前會話審批。`auto` 無需詢問客戶端即批准所有帶 `x-harness.approval` 的工具，並明確記錄每次批准；`ask` 恢復詢問，不帶引數顯示模式。審批框中連按兩次 `A` 設定同一模式，並批准當前會話已排隊請求；它也覆蓋程式形式呼叫，而單工具 `a` 不覆蓋，因此最寬授權需要額外確認。`a` 仍僅表示該會話始終允許此工具。
- `/workspace [path]` — 當前會話的主工作區。首次輪次固定啟動目錄；不帶引數顯示路徑、git 分支、是否為連結工作樹（及主樹路徑）、獲准根目錄數。路徑引數僅在尚無輪次的全新會話中允許重新固定；開始執行後工作區不可變，以保證恢復時路徑與凍結提示一致。之後 core 會拒絕並要求新建會話。它與 `/roots` 不同：工作區是相對路徑的主根，roots 額外授權其他根。底部用 `⧉` 標識連結工作樹。
- `/roots [add|remove <dir>]` — 管理會話工作目錄集合。不帶引數列出出生時固定的主工作區和額外根；add/remove 修改授權，底部顯示分支和 `+N`。授權會擴大工具與寫入圍欄允許範圍，直到明確移除，但不是 OS 沙箱。支援 `~`，相對路徑按工作區解析；core 要求絕對且存在的目錄。例如 `/roots add ~/git/other-clone`。
- `/components [all|direct|discovered|undiscovered]` — 按元件顯示當前會話可呼叫工具及暴露狀態。
- `/discover <component>|tool=NAME` — 將某元件的工具或一個具名工具暴露給當前會話，使模型可直接呼叫。
- `/recall <words…>` — 使用 store 搜尋索引上的 context_recall 排名檢索當前會話完整歷史，顯示帶訊息 ID 的受限長度單行結果，相當於面向使用者的記錄 grep。
- `/snippets [show|ins|del <n>]` — 管理 alt+拖動採集的片段（1 為最新）。show 顯示全文，ins 插入輸入框，del 刪除；片段是 kind `snippet` 的 store 文件，模型也能讀取。
- `/agent [name|none]` — 選擇**新會話**出生時使用的代理定義，包括 persona、初始工具集、模型、推理和預算。none 清除選擇；不帶引數開啟定義選擇器，顯示各組成部分。與 `/toolset` 一樣，選擇隻影響以後建立的會話，不重寫已有會話（框架固定出生身份）。標題欄顯示當前選擇。
- `/skills [name]` — 瀏覽框架已安裝技能並載入。選單顯示技能描述與來源根（`project`/`bundled`/`home`/`config`）；當前會話已載入項標記 `● ` 並置頂。選擇後加載，也可直接指定名稱。列表來自 skill_list，載入使用與模型相同的 skill_load，因此大小限制、發現和遮蔽規則只有一套實現；替代無法標識已載入狀態或互動載入的原始 JSON 列表。
- `/toolset [name|default]` — 選擇新會話初始工具集。default 清除選擇；不帶引數開啟選擇器，標記當前項並顯示解析後的工具數與 token 成本，default 顯示為標準工具集。`＋ New toolset…` 表單在儲存前對照實時目錄驗證選擇器：單獨名稱表示元件，單工具寫為 `component.tool`（如 `edit.read` 而非 `read`），`-tool` 排除。工具集不是安全邊界，只調整選擇生效期間建立的新會話工具。
- `/new [id]` — 建立新會話。
- `/session` — 瀏覽、切換、恢復或建立會話。**預設隱藏子代理會話**，避免每次委派產生的子會話擠滿列表。a 切換顯示，子會話用 `↳` 標記並顯示父會話；標題顯示隱藏數量。過濾框使用框架 store 的服務端 search（gokr/niffler#77），按 ID/標題檢索，不下載所有會話；舊框架無該工具時回退為本地過濾已載入列表。
- `/locale [en|zh|zh-TW]` — 切換並儲存介面語言。
- `/theme [name]` — 切換並儲存顏色主題；不帶引數開啟實時預覽選擇器，介面與 Markdown 樣式配套。除內建預設外，有十七種精選配色：淺色包括 `light`、`sepia`、`solarized-light`、`github-light`、`rose-pine-dawn`，另有 `solarized-dark`、`gruvbox-dark`、`nord`、`dracula`、`tokyo-night`、`catppuccin-mocha`、`monokai`、`one-dark`、`github-dark`、`rose-pine`、`kanagawa`、`everforest`。`NIF_TUI_THEME` 可覆蓋啟動預設值。
- `/mouse [on|off]` — 滑鼠跟蹤預設開啟，滾輪、單擊和普通拖動可同時用；關閉改用終端原生選擇，但沒有應用滾輪事件。
- `/cards [on|off]` — 使用主題卡片背景為工具呼叫組著色，使其呈現為整體（當前程序內顯示開關）。
- `/niffler [NAME|next|previous]` — 在程序內列出或切換例項。已訪問例項保留連線和會話狀態；驗證失敗保持原例項。`/niffler local` 返回本地。
- `/restart` — 以保留退出碼結束，讓安裝的啟動器重新執行更新後的二進位制；沒有啟動器時僅退出。會話及客戶端身份（`Niffler N`）傳給後繼程序，即使舊程序未釋放身份也能恢復。
- `/quit` — 與 Ctrl+C 一樣退出，正常釋放客戶端身份，不產生重啟交接。仍在執行的輪次由 session runner 繼續，下次連線從 store 重放。
- `/alias <name> <prompt…>` — 定義提示快捷方式。輸入 `/name` 時將儲存提示及額外引數作為普通輪次傳送。`/alias add <name> cli call <tool> '<json>'` 定義直接工具呼叫別名，`$1`…`$9` 插入呼叫引數。`/alias` 或 `/alias list` 列出，`/alias rm <name>` 刪除；定義儲存在使用者狀態目錄，支援 Tab，出現在 `/help`，不能遮蔽內建名稱。
- `/help` — 從註冊命令表生成摘要，始終列出所有內建及外掛命令。

Tab 補全命令名，也補全命令宣告支援的引數值（內建候選或延遲從宣告的源工具讀取）。未知命令會提示相近名稱。

### Shell escapes

兩個 shell 標記在當前會話工作區使用本程序環境和 `NIF_TUI=1`，執行使用者自己的 `$SHELL -c` 命令，並以暗色塊顯示輸出。它們是使用者自己的命令列，不需要匯流排連線或審批（審批針對模型呼叫而非使用者按鍵），也不傳送給框架。因為沒有附加終端，不支援 `vim`、`less`、互動式 ssh 等。遠端 `NIF_NATS_URL` 下仍在執行 TUI 的機器執行，而非框架機器。

- `!<command>` — 輸出僅留在本地記錄，類似另開終端執行 `!git status`、`!make test`。
- `?<command>` — 執行後將輸出作為輪次傳送給模型，例如 `?git status`。有執行輪次時作為引導輸入；執行中切換會話則不再發送提示，輸出仍保留，可重新提問。

輸出上限 8 KB，截斷時明確顯示 `… N bytes truncated`，超時限制掛起命令。單獨 `!` 或 `?` 是普通提示，否則標記優先：以它們開頭的行總按 shell 轉義處理，發給模型的句子不要這樣開頭。

`?` 也適用於本地命令：`?/status`、`?/doctor deep`、`?/recall <words>`、`?/info`、`?/components` 等。先執行命令，再按相同限制將顯示內容發給模型。這是詢問命令輸出的方式；舊的逐命令 ask 引數（如 `/status ask`）已移除。開啟選擇器/表單的命令（`/session`、`/theme`、`/profile`）沒有文字輸出，會拒絕該標記；`!` 不執行斜槓命令。

選擇器用 `/` 過濾、箭頭移動、Enter 選擇、Esc 返回。提供商表單遮蔽 API key；表單憑據不寫入輸入歷史或會話記錄。提交的普通提示和斜槓命令都會存入輸入歷史，因此上箭頭和 Ctrl+R 也能找回 `/status`、`/mcp search ...` 等。

標題欄顯示會話 ID、`think:`/`tool:`/`effort:`、實際提供商和模型及上下文儀表。上下文優先使用模型報告的總 token，透過會話元資料跨恢復儲存，在與 core 相同的 75% 警告 / 90% 裁剪閾值變色。還顯示累計 token（`↑ in ↓ out`）和從標題元資料恢復的提示快取命中率。提供商選擇修改全域性預設，模型選擇僅作用於當前會話。

輸入區域由橫線界定，旋轉圖示、`working`、停止中和兩步停止提示嵌入上邊線。底部單行顯示工作區和短暫提示；命令/按鍵提示放在 `/help` 和 Tab 補全中。

## Plugin slash commands

元件無需編寫 UI 程式碼即可宣告式擴充套件 TUI：正常註冊工具，並在註冊中加入 `slash`（`{name, description, tool, params[]}`），含引數補全來源，見 Niffler 倉庫 `docs/WIRE.md`。Core 驗證、將合併表儲存到 store，並在變化時釋出 `ev.catalog.updated`；TUI 先讀 store，再跟隨實時事件。

註冊命令按宣告解析位置引數、`name=value`、裸布林標記和預設值，呼叫目標工具，結果顯示為記錄中的元資訊塊。內建命令遮蔽同名註冊，元件離開時命令自動消失。

## Transcript and client state

助手回覆透過 Glamour 渲染 Markdown，支援標題、粗體、斜體、列表、表格和高亮程式碼塊。流式輸入時先顯示純文字，暫停時升級為 Markdown，避免每個 token 都完整渲染。

啟動或切換會話（`/session`、`/new`、瀏覽器選擇）時，從 store 訊息重建輸出：使用者/助手輪次、推理、帶引數結果的工具卡片、輪次錯誤和暗色 `▸` 系統通知行（子代理結算、後臺程序退出、自主喚醒提示）。歷史可見且可滾動。通知也實時到達；子任務完成喚醒父會話時會顯示結算和隨後的回覆，無需使用者詢問。子代理執行時顯示 `agent N (age)`，後臺程序顯示 `bg N`。

預設視口保留最後 3000 行渲染結果，較早內容隱藏處顯示標記。已完成塊被快取，僅重新渲染流式尾部；token 重繪合併，掃描不超過視窗，使長會話成本保持平穩。`NIF_TUI_SCROLLBACK` 設定行數，0 不限；完整記錄始終保留在 store。

Linux 和 macOS 的客戶端偏好及狀態統一位於 `~/.niffler/`：`theme`、`locale`、`aliases`、`activity.json`（不再有實例檔案：遠端路由來自 harness 的 fleet 註冊表）、按鍵值區分的 `session-*`/`handoff-*`，以及上限 200 條的 `history-<session>.jsonl`。刪除歷史檔案即可清空。不引入 SQLite。

首次使用從舊平臺配置目錄的 `niffler/` 和 `$XDG_STATE_HOME/niffler-tui/`（預設 `~/.local/state/niffler-tui/`）遷移。已有目標優先；遷移檔案許可權為 0600，成功後刪除原始檔。升級前關閉舊客戶端，它們仍寫舊位置。XDG 僅用於尋找舊檔案，不決定新目標。框架 store 資料和技能目錄不變。

Markdown 樣式遵循 `GLAMOUR_STYLE`（預設 `dark`，也可為 `light`、`ascii`、`dracula`、`tokyo-night` 等）。備用螢幕保持視口和輸入穩定；向上滾動後，新 token 不會強制跳到底部。

## Plugin status

`niffler.json` 將元件標記為 `"interactive": true`：外掛安裝只構建，使用者從終端手動啟動：

```bash
./var/bin/cli install gokr/niffler-tui
./var/bin/tui -session game
```

互動元件不受 Niffler 監督或自動重啟。更新/刪除外掛前退出執行中的 TUI。

### Lima migration

`lima` 配置使用 `limactl shell --workdir=/home/ubuntu NAME` 和 guest CLI 標準輸入輸出 relay，根為 `/home/ubuntu/niffler`。保留可選 SSH host 路由、直接 SSH 和 Incus。舊 Multipass 收據和 `multipass` 註冊欄位明確拒絕；應建立 Lima guest 並匯入其驗證收據，不要重新命名舊路由或複製身份。Fleet 固定 Ubuntu 摘要，停用掛載、憑據轉發、自動埠轉發，並啟用 guest 持久使用者服務。真實 Lima 冒煙測試仍需單獨授權。

## Build

開發 checkout 預期與 Niffler 相鄰，以便 go.mod 的本地 replacement 找到 Go SDK：

```bash
make            # 构建 bin/niffler-tui
make test       # 执行 TUI 测试
make run        # 构建并运行
```

或直接執行：

```bash
go build -o bin/niffler-tui ./tui
```

Niffler builder 不使用此 go.mod，而是建立隔離模組，replacement 指向其自己的 `sdk/go`。

## Fleet startup (experimental)

遠端實例來自 harness 自身的 fleet 註冊表（`fleet_instances`，store kind `fleetinstance`），其中保存了 harness 已驗證的路由與身分標識。客戶端不再有實例檔案：客戶端總是有自己的本機 harness，因此首次連線前無需記住任何東西。

已註冊的 peer 只需名稱：

```bash
./bin/niffler-tui --instance-probe worker
./bin/niffler-tui --instance worker
```

尚未註冊的 peer 在本次啟動時給出路由：

```bash
./bin/niffler-tui --instance worker --ssh ubuntu@worker
./bin/niffler-tui --instance worker --incus worker [--host operator@hypervisor]
./bin/niffler-tui --instance worker --lima worker
./bin/niffler-tui --instance worker --bus nats://127.0.0.1:4222
```

每次啟動只能有一個路由，身分由即時目錄得知——命令列參數從不宣告身分。guest 預設根為 `/home/ubuntu/niffler`；SSH 目標可用 `--instance-root` 覆寫。要在不帶參數時看到 peer，請在 harness 上註冊它（`fleet_install` 或 `fleet_instance_import`），它會出現在 `/niffler` 與環形切換中。

環形切換依註冊表輪轉：Alt+N / Alt+Shift+N 會跳過連線失敗的 peer，報告並嘗試下一個；全部失敗時停留在原實例並明確提示。

Incus 收據（`transport: "incus"`）透過 incus exec 和原生 relay 以普通 ubuntu 使用者連線，不用 guest SSH。根為 `/home/ubuntu/niffler`，明確選擇本地 daemon 和 default 專案，忽略客戶端可變預設值。Incus/Lima 可帶 `host: "gokr@wowbagger.krampe.se"`，管理命令經該主機 SSH 執行，每個引數都做 shell 引用；省略則本地管理 guest。

管理 API/NATS 無需公開暴露。SSH 金鑰和 known_hosts 屬於客戶端，不轉發 agent、不復制 guest 金鑰。部署/更新是經批准的框架 fleet 操作，不是 TUI 啟動副作用。

### Switching between instances

訪問過的每個例項保持獨立連線，切換在程序內完成，不重新執行；離開例項上的工作繼續執行。

| 輸入 | 效果 |
|---|---|
| `Alt+N` / `Alt+Shift+N` | 下一個 / 上一個例項，迴圈切換 |
| `/niffler` | 列出例項和狀態 |
| `/niffler NAME` | 切換到已註冊的 NAME |
| `/niffler next` / `previous` | 透過命令進行相同切換 |

每個例項獨立儲存會話、工作區、草稿、滾動位置和連線；同一 session ID 不被假定在另一個例項中代表相同會話。底部標記當前和已訪問例項：

```
● local   ○ worker ✓2   Alt+N next · Alt+Shift+N prev · /niffler
```

`●` 為當前螢幕例項，`✓N` 為離開時完成的會話數量，`!approval` 為等待人工，`…`/`⚠` 為連線中/斷開。`/niffler local` 返回本機，但會核對 core 根目錄，避免誤接其他例項。未檢視例項的審批僅顯示，不代為批准。

### Completion notices

其他例項的輪次完成會增加未讀標記；開啟會話後清除。讀取狀態只儲存在客戶端，與代理通知確認獨立；檢視完成會話既不消費模型待處理通知，也不啟動新輪次。同一會話多次完成合併為一個未讀標記。

連線和重連時分頁讀取框架持久完成快照，配合實時推送，避免離線期間完成丟失。被終止的輪次或記錄寫入失敗可能沒有收據；沒有收據表示未知，不表示成功。

### Remote specifics

要求框架 SDK 支援 `sdk.NewWithURL` 和 `Component.OnReconnected`；舊 SDK 編譯失敗而不是靜默退化。

遠端工作區預設收據根，而非筆記本 cwd。會話/重啟狀態按穩定 instance ID 而非轉發埠儲存，隧道換埠不影響歷史。遠端停用本地路徑補全和記錄檔案開啟；附件仍來自明確選擇的本地檔案。每個轉發連線在傳送 SDK 流量前重新解析並驗證固定目錄身份，重連也如此；不匹配關閉該連線，不回退。這是獨立探測連線，不是對惡意端點替換的身份認證。`/restart` 保留當前例項。後臺審批尚未實現。

## Architecture

Niffler 的 Bubble Tea 終端客戶端。它與桌面 UI 一樣是匯流排客戶端，透過 `svc.core.call` 驅動 session 呼叫，渲染 `ev.session.*` token、工具呼叫、助手回覆和完成事件。

元件以零工具註冊；執行客戶端不向 LLM 工具上下文新增內容。
