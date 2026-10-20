# niffler-tui

[English](README.md) · [简体中文](README.zh.md) · [繁體中文](README.zh-TW.md) · [Discord](https://discord.gg/ThJFEAJUAk)

> 🤖 AI 自動翻譯；以 [English](README.md) 為準。

[Niffler](https://github.com/gokr/niffler) 的終端聊天客戶端。Niffler 是能夠自我擴充的程式開發代理框架。在終端中與代理對話、查看工具執行過程，並繼續之前的會話。

- 串流 Markdown 回覆、推理內容和可摺疊工具卡片。
- 會話歷史、可搜尋的會話清單及提供者/模型選擇器。
- 檔案附件、選取複製和核准提示。
- 多種主題，以及英語、簡體中文和繁體中文介面。

## Get started

在 **Niffler 框架儲存庫**中安裝客戶端和啟動器：

```bash
make install WITH_TUI=1
```

在 Niffler 中設定模型提供者，然後在真正的終端中進入需要代理操作的專案：

```bash
cd /path/to/your/project
niffler-tui
```

安裝的啟動器會在需要時啟動本機框架。輸入請求，按 `Enter` 傳送。目前目錄成為新會話的工作區；如有該框架/工作區的已儲存會話，會自動恢復。
使用 `/new` 建立新會話，或用 `/session` 瀏覽歷史會話。

尚未設定提供者？使用 `/connect` 填寫連線資訊，或用 `/provider` 選擇已有提供者或訂閱登入。`/model` 為目前會話選擇模型。連線表單中的憑證會被遮蔽，不寫入聊天歷史。

## Everyday controls

| 按鍵 / 命令 | 操作 |
|---|---|
| `Enter` | 傳送；忙碌時引導目前回合 |
| `Alt+Enter` / `Ctrl+J` | 換行 |
| `Tab` | 補全命令與參數 |
| `Up` / `Down`、`Ctrl+R` | 回顧或搜尋輸入歷史 |
| `PgUp` / `PgDn` | 捲動會話 |
| `Ctrl+T` / `Ctrl+E` | 調整推理 / 工具卡片詳細程度 |
| `Esc`，再按 `Esc` | 確認停止目前回合 |
| `/new`、`/session` | 建立或切換會話 |
| `/theme`、`/locale` | 修改主題或語言 |
| `/help` | 查看內建及外掛命令 |
| `Ctrl+C` | 結束客戶端 |

滑鼠滾輪可捲動；拖曳可選取並複製文字。受保護的代理操作需要核准。結束客戶端不會停止正在執行的代理回合。

## Learn more

**[Manual](MANUAL.zh-TW.md)**提供完整按鍵和命令參考、設定、shell 跳脫、客戶端狀態遷移、開發說明，以及實驗性的多執行個體/fleet 支援。

Linux 和 macOS 上的客戶端偏好及狀態儲存在 `~/.niffler/`；會話記錄儲存在框架 store 中。詳見 [client state](MANUAL.zh-TW.md#transcript-and-client-state)。

## Build from source

透過本機 Go SDK replacement 關聯相符的 Niffler 儲存庫後：

```bash
make build      # bin/niffler-tui
make test
```

先啟動框架，再從終端執行 `./bin/niffler-tui`。僅建置二進位檔案不會安裝自動啟動/重啟啟動器。參見 [Build](MANUAL.zh-TW.md#build) 和 [plugin installation](MANUAL.zh-TW.md#plugin-status)。
