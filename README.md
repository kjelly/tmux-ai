# tmux-ai

用自然語言操作 tmux 的 CLI 工具。透過 [Ollama](https://ollama.com) 本地 LLM 的 function calling，讓 AI agent 直接控制 tmux 的 session、window、pane，執行指令、監控輸出、協調多個 AI agent。

---

## 目錄

- [需求](#需求)
- [安裝](#安裝)
- [快速開始](#快速開始)
- [全域設定](#全域設定)
- [指令參考](#指令參考)
  - [run](#run--執行一次性指令)
  - [repl](#repl--互動式對話)
  - [popup](#popup--tmux-快捷鍵彈出視窗)
  - [list](#list--查看目前-tmux-狀態)
  - [alias](#alias--pane-別名管理)
  - [watch](#watch--監控-pane-觸發動作)
  - [relay](#relay--將-pane-輸出轉送給-agent)
  - [broadcast](#broadcast--廣播同一指令到多個-pane)
  - [workflow](#workflow--多步驟-dag-工作流程)
  - [schedule](#schedule--排程指定時間執行指令)
  - [context](#context--全域-context-設定)
  - [pipe](#pipe--stdin-作為-context)
  - [ask](#ask--直接問-pane-裡的-agent)
  - [template](#template--儲存常用指令範本)
  - [follow](#follow--即時串流-pane-輸出)
- [Target 語法](#target-語法)
- [tmux.conf 快捷鍵設定](#tmuxconf-快捷鍵設定)
- [工作流程範例](#工作流程範例)

---

## 需求

- Go 1.21+
- tmux 3.0+（popup 功能需要 tmux 3.2+）
- [Ollama](https://ollama.com) 在本機執行，並下載至少一個支援 function calling 的模型

**推薦模型**（支援 function calling）：

```bash
ollama pull llama3.2        # 預設，輕量快速
ollama pull qwen2.5:14b     # 較強，繁中理解佳
ollama pull gemma3:27b
```

---

## 安裝

```bash
git clone https://github.com/llmore/tmux-ai
cd tmux-ai
go build -o tmux-ai .

# 移到 PATH
mv tmux-ai ~/bin/
# 或
sudo mv tmux-ai /usr/local/bin/
```

---

## 快速開始

確認 Ollama 已啟動且有模型：

```bash
ollama serve          # 若尚未啟動
ollama list           # 確認有模型
```

基本使用：

```bash
# 查看目前所有 tmux session/window/pane
tmux-ai list

# 執行一次性指令
tmux-ai run "在新 window 開啟 vim，並分割一個 shell pane 在下方"

# 指定模型
tmux-ai run --model qwen2.5:14b "切換到 sport session 的 crush window"

# 互動式 REPL
tmux-ai repl
```

---

## 全域設定

所有指令都支援以下 flags，可用環境變數設定預設值：

| Flag | 環境變數 | 預設值 | 說明 |
|------|----------|--------|------|
| `--model` | `OLLAMA_MODEL` | `llama3.2` | Ollama 模型名稱 |
| `--host` | `OLLAMA_HOST` | `http://localhost:11434` | Ollama 伺服器 URL |
| `--max-iterations` | — | `50` | Agent loop 最大迭代次數 |
| `--timeout` | — | `300` | 全域 timeout（秒） |
| `--verbose` | — | `false` | 顯示詳細 log |

建議在 shell profile 設定常用值：

```bash
# ~/.bashrc 或 ~/.zshrc 或 ~/.config/nushell/env.nu
export OLLAMA_MODEL=qwen2.5:14b
export OLLAMA_HOST=http://localhost:11434
```

---

## 指令參考

### `run` — 執行一次性指令

```
tmux-ai run ["instruction"] [flags]
```

對整個 tmux 環境下達一個自然語言指令，agent 會觀察狀態、呼叫工具、完成任務後回報。

**Flags：**

| Flag | 縮寫 | 說明 |
|------|------|------|
| `--pane` | `-p` | 指定主要操作的 pane（%ID、alias 或 session:window.pane） |
| `--session` | `-s` | 指定 session（可搭配 `--window`） |
| `--window` | `-w` | 指定 window index 或名稱（搭配 `--session`） |
| `--file` | `-f` | 從檔案讀取指令 |
| `--resume` | — | checkpoint 檔案路徑（中斷後繼續） |
| `--report` | — | 執行完後輸出 HTML 視覺化軌跡報告到指定路徑 |

**範例：**

```bash
# 基本指令
tmux-ai run "在 github session 的 window 3 執行 make build，等待完成"

# 執行後產出 HTML 報告（含完整決策與工具呼叫時間軸）
tmux-ai run "幫我整理 sport session 的狀態" --report /tmp/report.html

# 指定目標 pane
tmux-ai run --pane %25 "這個 pane 在跑什麼？幫我中斷並清除輸出"
tmux-ai run --pane claude-sport "請問你目前在做什麼任務"

# 用 --session + --window 指定目標
tmux-ai run --session sport --window 1 "告訴這個 agent 先暫停"
tmux-ai run -s github -w 3 "切換到這個 window"

# 從檔案執行複雜腳本
tmux-ai run --file setup-dev.txt

# 中斷後繼續
tmux-ai run --resume /tmp/checkpoint.json "繼續之前的任務"

# 指定模型
tmux-ai run --model gemma3:27b "幫我整理所有 session 的狀態"
```

**`--report` HTML 軌跡報告：**

指定 `--report <path.html>` 後，執行完成（無論成功或失敗）會自動產出一份 HTML 報告，包含：

- 完整的決策與工具呼叫**時間軸**
- 每個 LLM 回應、tool call 參數、tool 結果
- 執行成功 / 失敗狀態
- 步數統計

```bash
tmux-ai run "review sport session 的所有 agent 進度" --report ~/reports/review.html
open ~/reports/review.html   # macOS
xdg-open ~/reports/review.html  # Linux
```

---

### `repl` — 互動式對話

```
tmux-ai repl [flags]
```

啟動互動式 REPL，可以連續輸入指令而不需每次重啟程式。每個指令都有最新的 tmux 狀態作為 context。

**Flags：**

| Flag | 說明 |
|------|------|
| `--keep-history` | 保留對話歷史（適合多步驟任務） |

**內建指令：**

| 輸入 | 動作 |
|------|------|
| `list` | 顯示目前 tmux 狀態 |
| `exit` / `quit` | 離開 |
| 空行或 Ctrl+D | 離開 |

**範例：**

```bash
tmux-ai repl

# REPL 內：
tmux-ai> 查看所有 session 目前在做什麼
tmux-ai> 幫我在 github session 開一個新 window 跑 htop
tmux-ai> list
tmux-ai> exit
```

---

### `popup` — tmux 快捷鍵彈出視窗

```
tmux-ai popup [flags]
```

設計用於 `tmux display-popup` 的輕量互動介面。按快捷鍵彈出浮動視窗，輸入指令，完成後自動關閉。

**Flags：**

| Flag | 縮寫 | 說明 |
|------|------|------|
| `--pane` | `-p` | 目標 pane（tmux binding 傳入 `#{pane_id}`） |
| `--session` | `-s` | 目標 session |
| `--window` | `-w` | 目標 window |
| `--multi` | `-m` | 保持開啟，可連續輸入多個指令 |

**tmux.conf 設定：**

```bash
# 加入 ~/.tmux.conf

# i 鍵：單次指令，執行後自動關閉
bind-key i run-shell "tmux display-popup -E -w 80% -h 60% -b rounded -T ' tmux-ai ' \"tmux-ai popup -p #{pane_id}\""

# I 鍵：多次指令，手動關閉
bind-key I run-shell "tmux display-popup -E -w 80% -h 60% -b rounded -T ' tmux-ai ' \"tmux-ai popup -p #{pane_id} --multi\""

# Alt+a：不需要 prefix key
bind-key -n M-a run-shell "tmux display-popup -E -w 80% -h 60% -b rounded -T ' tmux-ai ' \"tmux-ai popup -p #{pane_id}\""
```

載入設定：

```bash
tmux source ~/.tmux.conf
```

> **注意：** `#{pane_id}` 由 tmux 在執行前展開，讓 agent 知道你是從哪個 pane 觸發 popup，自動設為操作目標。

Popup 介面：

```
 tmux-ai  │  sport:1  %24  [crush]  [busy]
────────────────────────────────────────────────────────────
❯ 請幫我整理這個 agent 的輸出並總結
```

> **tmux 版本需求：** `-b rounded` 需要 tmux 3.2+。若版本較舊，去掉 `-b rounded`。

---

### `list` — 查看目前 tmux 狀態

```
tmux-ai list
```

顯示所有 session、window、pane 的狀態，包含目前執行的程式和最後幾行輸出。

**輸出範例：**

```
Session: sport
  Window 1: crush [active]
    * %24  cmd=crush         [busy]
       |  How's the CLI experience?
       |  >
  Window 2: nvim
      %26  cmd=nvim          [busy]
       |   NORMAL  1   env.nu

Session: github
  Window 3: go [active]
    * %43  cmd=go            [busy]
       |  tmux-ai>
```

標記說明：
- `*` — 目前 active 的 pane
- `[active]` — 目前 active 的 window
- `[busy]` / `[idle]` — pane 是否在執行程式

---

### `alias` — Pane 別名管理

```
tmux-ai alias <subcommand>
```

為常用的 pane 設定好記的名稱，避免每次都要查 `%ID`。別名儲存於 `~/.config/tmux-ai/aliases.json`。

**子指令：**

#### `alias set`

```bash
# 把目前所在的 pane 設為別名（$TMUX_PANE 自動帶入）
tmux-ai alias set claude-sport

# 指定 pane ID
tmux-ai alias set build-pane %25

# 用 session:window 格式
tmux-ai alias set opencode-hufu "hufu:1"
```

#### `alias list`

```bash
tmux-ai alias list
```

輸出範例：

```
  NAME             PANE    RUNNING
  ----             ----    -------
  build-pane       %25     make
  claude-sport     %27     claude
  opencode-hufu  %19     opencode
```

#### `alias delete`

```bash
tmux-ai alias delete claude-sport
```

**別名在所有指令都可使用：**

```bash
tmux-ai run --pane claude-sport "你目前的任務進度如何"
tmux-ai watch build-pane "error:" --then "修正這個錯誤" --pane claude-sport
tmux-ai relay "修正 build 錯誤" --from build-pane --to claude-sport
tmux-ai broadcast "總結進度" --panes "claude-sport,opencode-hufu"
```

---

### `watch` — 監控 pane，觸發動作

```
tmux-ai watch <pane> <pattern> [flags]
```

輪詢指定 pane，當內容符合 pattern（字串或 regex）時執行動作。觸發時，pane 的內容會自動作為 context 傳給 `--then` 的 agent。

**Flags：**

| Flag | 預設 | 說明 |
|------|------|------|
| `--notify` | — | pattern 符合時顯示通知訊息 |
| `--then` | — | pattern 符合時執行的 AI 指令（pane 內容作為 context） |
| `--pane` | 監控的 pane | `--then` 的目標 pane |
| `--keys` | — | pattern 符合時送出的原始按鍵（`\n` 代表 Enter） |
| `--absent` | — | 反轉觸發條件：等文字**消失**才觸發（預設等文字出現） |
| `--range` | — | Python slice 語法，只看指定範圍的行（如 `[-5:]`）；省略 `--pattern` 時只要範圍內有**任何變化**就觸發 |
| `--interval` | `2000` | 輪詢間隔（毫秒） |
| `--once` | `true` | 符合一次後停止 |
| `--lines` | `50` | 擷取的 pane 行數（建議設大於 range 最大需求） |

**範例：**

```bash
# build 完成時通知
tmux-ai watch build-pane "Build succeeded" --notify "Build 完成！"

# 出現錯誤時請 claude 修正
tmux-ai watch build-pane "error:" \
  --then "修正上方的 build 錯誤" \
  --pane claude-sport

# 等 "Building..." 消失後再做事（--absent）
tmux-ai watch build-pane "Building..." --absent \
  --then "build 完成，幫我跑測試" \
  --pane claude-sport

# 等 spinner 消失後通知
tmux-ai watch deploy-pane "Deploying" --absent --notify "Deploy 完成！"

# 自動回答確認提示
tmux-ai watch deploy-pane "Continue? \[y/N\]" --keys "y"

# 持續監控，不停止
tmux-ai watch %25 "FAIL|ERROR" --notify "有東西失敗了" --once=false

# 等 build-pane 最後 5 行有任何變化（不需要指定 pattern）
tmux-ai watch build-pane --range "[-5:]" \
  --then "最後幾行有變化，幫我分析" --pane claude-sport

# 只在最後 10 行中尋找 pattern
tmux-ai watch build-pane "error" --range "[-10:]" \
  --then "修正這個 build 錯誤" --pane claude-sport

# 監控特定行範圍有無變化（--lines 需夠大）
tmux-ai watch status-pane --range "[0:3]" --lines 20 \
  --notify "狀態列有更新！"

# [~:] 動態追蹤：有任何行變化就把「變化起點到最後」傳給 agent
tmux-ai watch log-pane --range "[~:]" --lines 200 \
  --then "幫我分析剛才新增的 log" --pane claude-sport

# Regex pattern
tmux-ai watch build-pane "^(ERROR|FATAL):" \
  --then "分析並修正這個嚴重錯誤" \
  --pane claude-sport \
  --interval 1000
```

**`--range` slice 語法：**

| 範例 | 說明 |
|------|------|
| `[-5:]` | 最後 5 行 |
| `[:5]` | 前 5 行 |
| `[10:20]` | 第 10–19 行（0-based） |
| `[-10:-3]` | 倒數第 10 到倒數第 4 行 |
| `[5:]` | 第 5 行到最後 |
| `[~:]` | **動態**：從第一行有變化的地方到最後（自動找 diff 起點） |

> `[~:]` 是特殊 sentinel：程式會記錄上次擷取的完整內容，偵測到任何行改變時，自動以**最先發生變化的行號**作為起始 index，回傳從那行開始的所有內容。`--lines` 決定擷取多少行，range 的計算基於擷取到的行數。

---

### `relay` — 將 pane 輸出轉送給 agent

```
tmux-ai relay <instruction> --from <pane> --to <pane> [flags]
```

監控 `--from` pane，擷取其內容作為 context，執行 instruction 並讓 agent 在 `--to` pane 操作。

適合「A pane 的輸出 → B agent 處理」的工作流程。

**Flags：**

| Flag | 預設 | 說明 |
|------|------|------|
| `--from` | （必填） | 來源 pane（ID 或 alias） |
| `--to` | （必填） | 目標 agent pane（ID 或 alias） |
| `--trigger` | — | 等到此 pattern 出現才觸發（省略則立即執行或偵測新內容） |
| `--continuous` | `false` | 持續運作，每次有新內容就 relay |
| `--interval` | `3000` | 輪詢間隔（毫秒，搭配 `--trigger` 或 `--continuous`） |
| `--lines` | `80` | 擷取來源 pane 的行數 |

**範例：**

```bash
# 立即擷取 %25 的內容，讓 %27 的 agent 處理
tmux-ai relay "檢查這個輸出並執行建議的修正" --from %25 --to %27

# 等 build 出錯，請 claude 修正
tmux-ai relay "修正上方的 build 錯誤" \
  --from build-pane --to claude-sport \
  --trigger "error:"

# 持續協作：opencode 有新輸出就轉給 claude 決定下一步
tmux-ai relay "根據最新輸出決定下一步行動" \
  --from opencode-hufu --to claude-sport \
  --continuous --interval 5000
```

---

### `broadcast` — 廣播同一指令到多個 pane

```
tmux-ai broadcast <instruction> [flags]
```

對多個 pane 同時執行相同的 AI 指令，預設平行執行。每個 agent 都以對應的 pane 作為主要操作目標。

**Target 選擇（至少一個）：**

| Flag | 說明 |
|------|------|
| `--panes` | 逗號分隔的 pane ID 或 alias |
| `--session` | 指定 session 的所有 pane |
| `--process` | 執行指定程式名稱的所有 pane |
| `--sequential` | 依序執行（預設平行） |

可以組合多個 selector，重複的 pane 自動去重。

**範例：**

```bash
# 詢問所有 claude session 的進度
tmux-ai broadcast "用一句話總結你目前的任務" --process claude

# 對特定 pane 廣播
tmux-ai broadcast "執行測試並回報結果" --panes "build1,build2,%33"

# 整個 session 的 pane
tmux-ai broadcast "完成目前的工作並等候指示" --session github

# 組合：所有 opencode + 特定 pane
tmux-ai broadcast "總結進度" --process opencode --panes extra-pane

# 依序執行
tmux-ai broadcast "git status 並整理未提交的變更" \
  --session github --sequential
```

---

### `workflow` — 多步驟 DAG 工作流程

```
tmux-ai workflow run <file.yaml> [flags]
tmux-ai workflow validate <file.yaml>
```

以 YAML 定義多步驟自動化工作流程。受 [Argo Workflows](https://argoproj.github.io/workflows/) 設計啟發，支援 DAG 依賴、條件執行、步驟間傳遞 context。

**概念對照：**

| Argo | tmux-ai workflow |
|------|-----------------|
| Template | step `type` |
| DAG / Steps | `depends:` 自動形成依賴圖 |
| Artifacts | `context_from:` 傳遞 step output |
| Parameters | `params:` + `{{params.key}}` |
| Exit Handler | `when: always` + `on_failure: continue` |
| Conditions | `when: contains(steps.X.output, "text")` |

**Step 類型：**

| type | 說明 |
|------|------|
| `run` | Agent 執行自然語言指令（預設） |
| `watch` | 輪詢 pane 直到 pattern 出現，output 是捕獲內容 |
| `shell` | 直接執行 bash 指令（不經 LLM） |
| `launch` | 開新視窗、啟動程式、等待就緒、送入指令（不經 LLM） |
| `wait` | 等待到自然語言指定的時間點（不經 LLM） |
| `relay` | 擷取 from pane → agent 在 to pane 操作 |
| `broadcast` | 同一指令平行發給多個 pane |

**Template 語法：**

| 語法 | 說明 |
|------|------|
| `{{params.KEY}}` | 代入 workflow params（或 `--param KEY=value` 覆寫） |
| `{{steps.NAME.output}}` | 代入指定步驟的 output（用於 `depends:` 之後的步驟） |

**`launch` step 欄位：**

| 欄位 | 說明 |
|------|------|
| `window` | 新視窗名稱 |
| `cwd` | 工作目錄（支援 `{{steps.NAME.output}}`） |
| `command` | 要啟動的程式（e.g. `claude`） |
| `pattern` | 等待此字串出現才代表程式就緒 |
| `timeout` | 等待 pattern 的秒數（預設 30） |
| `input` | 就緒後送入的文字（自動加 Enter） |

```yaml
- name: open-claude-review
  type: launch
  window: pr-review
  cwd: "{{steps.clone-repo.output}}"   # 取上一步 shell 的 echo 輸出
  command: claude
  pattern: ">"
  timeout: 30
  input: "/review https://github.com/owner/repo/pull/42"
```

**`wait` step 欄位：**

| 欄位 | 說明 |
|------|------|
| `until` | 自然語言時間表達式（支援與 `schedule` 相同的所有格式） |

```yaml
- name: wait-until-tonight
  type: wait
  until: "今晚七點"

- name: run-after-wait
  type: run
  pane: "{{params.claude_pane}}"
  instruction: "開始進行每日 code review"
  depends: [wait-until-tonight]
```

**`when` 條件：**

```yaml
when: always                                       # 無論如何都執行
when: on_success                                   # 預設：所有 deps 成功
when: on_failure                                   # 任何 dep 失敗才執行
when: "contains(steps.STEP_NAME.output, 'error')" # output 包含文字
when: "not_contains(steps.STEP_NAME.output, 'ok')"
```

**Flags（`workflow run`）：**

| Flag | 說明 |
|------|------|
| `--param key=value` | 覆寫 workflow params（可重複使用） |

**範例：**

```bash
# 驗證 workflow（不執行）
tmux-ai workflow validate examples/build-and-fix.yaml

# 執行
tmux-ai workflow run examples/build-and-fix.yaml

# 覆寫 params
tmux-ai workflow run examples/build-and-fix.yaml \
  --param build_pane=%25 \
  --param claude_pane=%27 \
  --param build_cmd="go build ./..."

# 指定模型
tmux-ai workflow run examples/multi-agent-review.yaml --model qwen2.5:14b
```

**Workflow YAML 格式：**

```yaml
name: build-and-fix
description: 執行 build，失敗時自動請 claude 修正

params:
  build_pane: build-pane    # 可用 --param 覆寫
  claude_pane: claude-sport
  build_cmd: "make build"

steps:
  # 無 depends 的 steps 自動平行執行
  - name: run-build
    type: run
    pane: "{{params.build_pane}}"
    instruction: "執行 {{params.build_cmd}} 並等待完成"

  # watch：輪詢直到 pattern 出現，output 供後續步驟使用
  - name: watch-result
    type: watch
    pane: "{{params.build_pane}}"
    pattern: "error:|Build succeeded"
    timeout: 120
    depends: [run-build]

  # watch + absent：等 "Building..." 消失後才繼續
  - name: wait-build-done
    type: watch
    pane: "{{params.build_pane}}"
    pattern: "Building..."
    absent: true              # 等文字消失
    timeout: 120
    depends: [run-build]

  # watch + range：最後 5 行有任何變化就觸發（不需要 pattern）
  - name: watch-output-tail
    type: watch
    pane: "{{params.build_pane}}"
    range: "[-5:]"            # Python slice，只看最後 5 行
    lines: 100                # 擷取 100 行供 slice 計算
    timeout: 60
    depends: [run-build]

  # watch + [~:] 動態追蹤：自動從最先變化的行開始，output 只含新增/變更內容
  - name: watch-new-output
    type: watch
    pane: "{{params.build_pane}}"
    range: "[~:]"             # 從第一行有變化的地方到結尾
    lines: 200
    timeout: 60
    depends: [run-build]

  # watch + range + pattern：只在最後 10 行中找 error
  - name: watch-error-in-tail
    type: watch
    pane: "{{params.build_pane}}"
    pattern: "error:"
    range: "[-10:]"
    timeout: 120
    depends: [run-build]

  # 條件執行：只在 build 失敗時執行
  - name: fix-errors
    type: run
    pane: "{{params.claude_pane}}"
    instruction: "修正上方的 build 錯誤"
    context_from: watch-result      # 把 watch-result 的 output 作為 context
    when: "contains(steps.watch-result.output, 'error:')"
    depends: [watch-result]

  # shell step：不需要 LLM
  - name: notify
    type: shell
    command: 'notify-send "Build" "Workflow complete"'
    when: always                    # 無論成敗都執行
    depends: [fix-errors]
    on_failure: continue            # 此步驟失敗不中止整個 workflow
```

**DAG 平行執行範例（`examples/multi-agent-review.yaml`）：**

```yaml
name: multi-agent-review
description: 兩個 agent 平行 review，完成後彙整

steps:
  # review-correctness 和 review-security 沒有相互依賴 → 自動平行
  - name: review-correctness
    type: run
    pane: "{{params.reviewer1}}"
    instruction: "review git diff 的 correctness"

  - name: review-security
    type: run
    pane: "{{params.reviewer2}}"
    instruction: "review git diff 的 security"

  # sync point：兩個都完成才執行
  - name: summarize
    type: run
    pane: "{{params.summary_pane}}"
    instruction: "根據以下兩份 review 整理 action items"
    context_from:
      - review-correctness
      - review-security
    depends: [review-correctness, review-security]
```

內建範例位於 `examples/` 目錄：

| 檔案 | 說明 |
|------|------|
| `examples/build-and-fix.yaml` | Build → 監控結果 → 失敗時請 claude 修正 |
| `examples/multi-agent-review.yaml` | 兩個 agent 平行 review → 彙整結論 |
| `examples/deploy-pipeline.yaml` | Build → Test → Deploy，失敗時 rollback |
| `examples/review-pr.yaml` | Clone GitHub PR repo → 在新視窗啟動 Claude Code review |

---

### `schedule` — 排程，指定時間執行指令

```
tmux-ai schedule <time-expr> <instruction> [flags]
```

解析自然語言時間，等到該時刻再讓 agent 執行指令。程式在前景等待，時間到即執行。

**支援的時間格式：**

| 格式 | 範例 |
|------|------|
| 中文相對（N 分鐘/小時/秒後） | `10分鐘後`、`2小時後`、`半小時後`、`30秒後` |
| 中文絕對（今天） | `今晚七點`、`下午三點`、`早上九點`、`下午三點半` |
| 中文絕對（指定日） | `明天早上九點`、`明天晚上八點`、`後天下午三點` |
| 英文相對 | `in 10 minutes`、`in 2 hours` |
| 英文絕對 | `at 9pm`、`tonight at 7`、`tomorrow at 9am` |

**Flags：**

| Flag | 縮寫 | 說明 |
|------|------|------|
| `--pane` | `-p` | 目標 pane（%ID、alias 或 session:window.pane） |
| `--session` | `-s` | 目標 session |
| `--window` | `-w` | 目標 window（搭配 `--session`） |

**範例：**

```bash
# 10 分鐘後執行 build
tmux-ai schedule "10分鐘後" "執行 make build 並等待完成"

# 今晚七點提醒 code review
tmux-ai schedule "今晚七點" "提醒進行 code review" --pane claude-sport

# 明天早上九點開始跑測試
tmux-ai schedule "明天早上九點" "執行完整測試套件" --session github --window 3

# 半小時後部署
tmux-ai schedule "半小時後" "執行 deploy pipeline"
```

**輸出範例：**

```
[schedule] 將在 35 分鐘後（15:30）執行
           指令：執行 make build 並等待完成

[schedule] 時間到，開始執行...
```

---

### `context` — 全域 Context 設定

```
tmux-ai context <subcommand>
```

管理 `~/.config/tmux-ai/context.md`。此檔案的內容會自動 prepend 到每一次 `run` / `repl` / `popup` / `pipe` / `template run` 的 LLM 指令中，不需要每次重複說明背景。

**子指令：**

| 指令 | 說明 |
|------|------|
| `context show` | 顯示目前的 global context |
| `context edit` | 用 `$EDITOR` 開啟編輯 |
| `context set "text"` | 直接設定內容 |
| `context clear` | 刪除 context 檔案 |

**範例：**

```bash
# 設定一次，之後所有指令都會帶上這個背景
tmux-ai context set "工作環境：Go 專案。主要 agent：claude-sport。session sport 是主要工作區，github 是 CI。回答用繁體中文，盡量簡潔。"

# 確認設定
tmux-ai context show

# 用編輯器修改（支援 Markdown）
tmux-ai context edit

# 之後的指令自動帶上 context（不需要額外設定）
tmux-ai run "幫我整理一下目前的狀態"
tmux-ai popup -p claude-sport
```

---

### `pipe` — Stdin 作為 Context

```
tmux-ai pipe <instruction> [flags]
```

讀取 stdin 作為 context，傳給 LLM agent。適合把其他指令的輸出直接接入 tmux-ai。

**Flags：**

| Flag | 縮寫 | 說明 |
|------|------|------|
| `--pane` | `-p` | 目標 pane |
| `--session` | `-s` | 目標 session |
| `--window` | `-w` | 目標 window |
| `--label` | — | stdin 內容的標題（預設 `stdin`） |

**範例：**

```bash
# Review git diff
git diff | tmux-ai pipe "review 這個 diff，找出 correctness 和 security 問題" --pane claude-sport

# 分析 build log
cat build.log | tmux-ai pipe "這個 build 失敗的原因是什麼，如何修正"

# 從 list 找問題
tmux-ai list | tmux-ai pipe "哪些 agent 看起來卡住了或有異常"

# kubectl 日誌分析
kubectl logs pod/myapp --tail=100 | tmux-ai pipe "總結錯誤" --label "pod logs"

# 傳給特定 pane 的 agent 處理
cat error.log | tmux-ai pipe "修正這些錯誤" -p claude-sport -s github -w 3
```

---

### `ask` — 直接問 Pane 裡的 Agent

```
tmux-ai ask <pane> <question> [flags]
```

直接把文字輸入到 pane（像使用者打字），等候回應穩定後擷取輸出。**不使用本地 LLM**，適合查詢已經在 pane 裡運行的 AI agent（claude、opencode、agy 等）。

回應判定為完成的條件：pane 內容在連續 `--stable-polls` 次輪詢中維持不變。

**Flags：**

| Flag | 預設 | 說明 |
|------|------|------|
| `--wait` | — | 等到此 pattern 出現才擷取（省略則用穩定偵測） |
| `--timeout` | `30` | 最長等待秒數 |
| `--lines` | `100` | 擷取的 pane 行數 |
| `--stable-polls` | `3` | 幾次連續不變才算完成 |
| `--interval` | `500` | 輪詢間隔（毫秒） |
| `--no-enter` | — | 不自動送出 Enter |

**範例：**

```bash
# 問 claude 目前狀態
tmux-ai ask claude-sport "你目前在做什麼，還需要多久？"

# 問 opencode 最近的變更
tmux-ai ask opencode-linker "用一句話描述你最後做的修改"

# 等特定 pattern 出現
tmux-ai ask build-pane "make build" --wait "Build succeeded|error:"

# 腳本化查詢所有 claude session
for pane in $(tmux-ai alias list | grep claude | awk '{print $2}'); do
  echo "=== $pane ===" && tmux-ai ask "$pane" "目前任務進度"
done
```

---

### `template` — 儲存常用指令範本

```
tmux-ai template <subcommand>
```

將常用指令存成命名範本，避免重複輸入。儲存於 `~/.config/tmux-ai/templates.json`。

**子指令：**

| 指令 | 說明 |
|------|------|
| `template save <name> "<instruction>"` | 儲存範本（省略 instruction 從 stdin 讀取） |
| `template list` | 列出所有範本 |
| `template run <name> [--pane ...]` | 執行範本（支援 `--pane` / `-s` / `-w`） |
| `template get <name>` | 印出 instruction（供 shell 替換） |
| `template delete <name>` | 刪除範本 |

**範例：**

```bash
# 儲存常用範本
tmux-ai template save code-review \
  "review 最新的 git diff，列出 correctness、security 和 edge case 問題"

tmux-ai template save morning-check \
  "告訴我你昨晚做了什麼，還有什麼待完成的任務，用條列式回答"

tmux-ai template save wrap-up \
  "完成目前的工作，整理輸出，等候下一個指示"

# 列出所有範本
tmux-ai template list

# 執行範本
tmux-ai template run code-review --pane claude-sport
tmux-ai template run morning-check -s sport -w 2

# 配合 broadcast 使用
tmux-ai broadcast "$(tmux-ai template get morning-check)" --process claude

# 從 stdin 儲存多行範本
cat << 'EOF' | tmux-ai template save deploy-check
檢查 deploy 是否成功：
1. 確認 pod 都是 Running 狀態
2. 檢查 error log
3. 回報結果
EOF
```

---

### `follow` — 即時串流 Pane 輸出

```
tmux-ai follow <pane> [flags]
```

即時輸出 pane 的新增內容，類似 `tail -f`。**本身不使用 LLM**；可透過 `--until` + `--then` 在條件符合時觸發 AI 指令。

**Flags：**

| Flag | 預設 | 說明 |
|------|------|------|
| `--interval` | `500` | 輪詢間隔（毫秒） |
| `--until` | — | 出現此 pattern 時停止 |
| `--notify` | — | `--until` 符合時顯示通知 |
| `--then` | — | `--until` 符合時執行 AI 指令 |
| `--pane` | 監控的 pane | `--then` 的目標 pane |
| `--lines` | `3000` | scrollback 行數 |
| `--from-start` | `false` | 也印出現有內容（預設從現在開始） |

**範例：**

```bash
# 監控 build 輸出（無 LLM）
tmux-ai follow build-pane

# 篩選 error
tmux-ai follow build-pane | grep -i "error\|warning"

# 等 build 結束後通知
tmux-ai follow build-pane --until "Build succeeded|FAILED" --notify "Build 完成"

# 等 build 失敗，請 claude 修正（有 LLM）
tmux-ai follow build-pane \
  --until "error:" \
  --then "修正這個 build 錯誤" \
  --pane claude-sport

# 包含現有內容一起輸出
tmux-ai follow %25 --from-start | tee build-$(date +%s).log
```

> `follow` 與 `watch` 的差異：`follow` 輸出新增的行（streaming），`watch` 只在 pattern 出現時觸發動作（event-driven）。

---

## Target 語法

所有接受 pane 的參數（`--pane`, `--from`, `--to`, `watch <pane>`, `--panes`）都支援以下格式：

| 格式 | 範例 | 說明 |
|------|------|------|
| `%ID` | `%27` | tmux pane ID（從 `list` 查詢） |
| `alias` | `claude-sport` | 用 `alias set` 設定的名稱 |
| `session:window` | `sport:1` | session 名稱 + window index |
| `session:window.pane` | `sport:1.0` | 精確到 pane index |
| `session:window-name` | `sport:crush` | 用 window 名稱（需唯一） |

> **建議：** 用 `tmux-ai list` 查詢 pane ID，再用 `alias set` 設定好記的名稱。

---

## tmux.conf 快捷鍵設定

完整的建議設定：

```bash
# ~/.tmux.conf

# prefix + i：單次 popup（執行後自動關閉）
bind-key i run-shell "tmux display-popup -E -w 80% -h 60% -b rounded -T ' tmux-ai ' \"tmux-ai popup -p #{pane_id}\""

# prefix + I：多次 popup（手動關閉）
bind-key I run-shell "tmux display-popup -E -w 80% -h 60% -b rounded -T ' tmux-ai ' \"tmux-ai popup -p #{pane_id} --multi\""

# Alt+a：不需要 prefix，隨時呼叫
bind-key -n M-a run-shell "tmux display-popup -E -w 80% -h 60% -b rounded -T ' tmux-ai ' \"tmux-ai popup -p #{pane_id}\""

# 指定模型的快捷鍵
bind-key A run-shell "tmux display-popup -E -w 80% -h 60% -b rounded -T ' tmux-ai (qwen) ' \"tmux-ai popup -p #{pane_id} --model qwen2.5:14b\""
```

```bash
# 載入設定
tmux source ~/.tmux.conf
```

> `-b rounded` 需要 tmux 3.2+。若版本較舊，去掉 `-b rounded`。

---

## 工作流程範例

### 日常開發

```bash
# 1. 查看全部狀態
tmux-ai list

# 2. 設定常用別名
tmux-ai alias set dev-shell %5
tmux-ai alias set claude-main %27
tmux-ai alias set build-log %25

# 3. 讓 AI 建立開發環境
tmux-ai run "在 dev-shell 切換到 ~/projects/myapp，分割下方 pane 執行 npm run dev"

# 4. 監控 build，出錯自動請 claude 修
tmux-ai watch build-log "error" \
  --then "修正 build 錯誤" \
  --pane claude-main &
```

### 管理多個 AI Agent

```bash
# 設定所有 agent 的別名
tmux-ai alias set claude-sport "sport:2"
tmux-ai alias set opencode-hufu "hufu:1"
tmux-ai alias set agy-github "github:6"

# 詢問所有 claude 的進度
tmux-ai broadcast "用一句話描述你目前的任務和進度" --process claude

# 讓 opencode 的輸出持續轉給 claude 決策
tmux-ai relay "根據 opencode 的輸出決定下一步" \
  --from opencode-hufu --to claude-sport \
  --continuous --interval 10000 &

# 當某個 agent 完成，通知並觸發下一步
tmux-ai watch opencode-hufu "task complete" \
  --notify "opencode 完成了！" \
  --then "review 這個結果並告知是否可以 merge" \
  --pane claude-sport
```

### 跨 Session 協作

```bash
# 把 github session 的 build 結果轉送給 sport session 的 claude
tmux-ai relay "分析 build 結果，如果有錯誤請修正" \
  --from "github:3" --to "sport:2" \
  --trigger "Build finished"

# 所有 session 收工
tmux-ai broadcast "完成目前任務，整理輸出，等候下一個指示" \
  --session github \
  --session sport
```

### 每日例行設定

```bash
# 設定一次 global context（之後不用再說）
tmux-ai context set "工作環境：Go 專案。主要 agent：claude-sport。回答用繁體中文，簡潔。"

# 儲存常用範本
tmux-ai template save morning "告訴我你昨晚做了什麼，還有什麼待完成"
tmux-ai template save review "review 最新 git diff 的 correctness 和 security"
tmux-ai template save wrap-up "完成目前工作，等候指示"

# 早上查所有 agent 狀態（不需要 LLM）
tmux-ai broadcast "$(tmux-ai template get morning)" --process claude
```

### 用 `ask` 腳本化查詢 Agent

```bash
# 查所有 claude session 的進度
tmux-ai alias list | awk '/claude/{print $2}' | while read pane; do
  echo "=== $pane ==="
  tmux-ai ask "$pane" "目前任務進度，一句話"
done

# 把結果彙整給一個 agent
tmux-ai ask claude-sport "目前任務進度" | tmux-ai pipe "這是 claude-sport 的回覆，幫我記錄下來"
```

### 用 `pipe` 整合外部工具

```bash
# Code review workflow
git diff HEAD~1 | tmux-ai pipe "review 這個 commit" --pane claude-sport

# 分析 CI log
gh run view --log | tail -100 | tmux-ai pipe "CI 失敗的原因是什麼"

# 結合 follow + pipe
tmux-ai follow build-pane --until "FAILED" | \
  tmux-ai pipe "修正這個 build 失敗" --pane claude-sport
```

### Workflow 自動化 Pipeline

```bash
# Build 失敗自動修正
tmux-ai workflow run examples/build-and-fix.yaml \
  --param build_pane=build-pane \
  --param claude_pane=claude-sport

# 平行 code review
tmux-ai workflow run examples/multi-agent-review.yaml \
  --param reviewer1=claude-sport \
  --param reviewer2=opencode-linker \
  --param summary_pane=claude-main \
  --model qwen2.5:14b

# Deploy pipeline
tmux-ai workflow run examples/deploy-pipeline.yaml \
  --param dev_pane=dev-shell \
  --param service=myapp

# Clone GitHub PR 並在新視窗啟動 Claude Code review
tmux-ai workflow run examples/review-pr.yaml \
  --param pr_url=https://github.com/owner/repo/pull/42

# 自訂視窗名稱
tmux-ai workflow run examples/review-pr.yaml \
  --param pr_url=https://github.com/owner/repo/pull/42 \
  --param window_name=my-review
```

### Popup 快速操作

按設定的快捷鍵（例如 `prefix + i`）彈出視窗後：

```
 tmux-ai  │  github:3  %43  [go]  [busy]
────────────────────────────────────────────────────────────
❯ 這個 pane 在做什麼？有沒有錯誤？
```

---

## Agent 工具清單

tmux-ai 提供以下工具給 LLM 使用（透過 Ollama function calling）：

| 工具 | 說明 |
|------|------|
| `capture_pane` | 擷取 pane 畫面內容（含 ANSI） |
| `capture_pane_clean` | 擷取乾淨文字（去除 ANSI） |
| `send_keys` | 輸入按鍵到 pane（自動加 Enter） |
| `send_lines` | 傳送多行內容（不加 Enter） |
| `send_keys_and_wait` | 輸入按鍵並等待 pattern 出現 |
| `wait_for_pattern` | 等待 pane 出現特定 pattern |
| `abort_command` | 送出 Ctrl+C 中斷指令 |
| `list_panes` | 列出所有 pane |
| `list_windows` | 列出所有 window |
| `new_window` | 開新 window（支援 `working_dir` 設定工作目錄） |
| `split_pane` | 分割 pane |
| `select_pane` | 切換到指定 pane |
| `select_window` | 切換到指定 window（跨 session） |
| `kill_pane` | 關閉 pane |
| `kill_window` | 關閉 window |
| `check_pane_idle` | 確認 pane 是否閒置 |
| `find_pane_by_process` | 依程式名稱找 pane |
| `search_pane_history` | 搜尋 pane scrollback 歷史 |
| `clear_pane_history` | 清除 pane scrollback，避免 `wait_for_pattern` 誤觸舊輸出 |
| `run_local_command` | 在本機執行 shell 指令 |
| `read_file` | 讀取檔案 |
| `write_file` | 寫入檔案 |
| `replace_text_in_file` | 取代檔案中特定文字 |
| `git_status_summary` | 取得 git 狀態摘要 |
| `log_progress` | 在 console 輸出進度訊息 |
| `ask_user` | 向使用者提問（需要決策時） |
| `send_notification` | 送出系統通知 |
| `sleep` | 等待指定毫秒 |
| `finish` | 標記任務完成 |
