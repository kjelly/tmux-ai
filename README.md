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
  - [pingpong](#pingpong--兩個-agent-交替執行)
  - [coordinate](#coordinate--多-window-角色路由)
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
ollama pull gemma4:31b      # 預設
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
| `--config` | `TMUX_AI_CONFIG` | `$XDG_CONFIG_HOME/tmux-ai/config.yaml` | 使用者全域設定檔路徑 |
| `--model` | `OLLAMA_MODEL` | `gemma4:31b` | Ollama 模型名稱 |
| `--host` | `OLLAMA_HOST` | `http://localhost:11434` | Ollama 伺服器 URL |
| `--max-iterations` | — | `50` | Agent loop 最大迭代次數 |
| `--timeout` | — | `300` | 全域 timeout（秒） |
| `--verbose` | — | `false` | 顯示詳細 log |

在 Linux 上，tmux-ai 依 [XDG Base Directory](https://specifications.freedesktop.org/basedir-spec/latest/)
慣例讀取 `$XDG_CONFIG_HOME/tmux-ai/config.yaml`；未設定 `XDG_CONFIG_HOME` 時就是
`~/.config/tmux-ai/config.yaml`。可從 [`examples/config.yaml`](examples/config.yaml)
建立設定檔：

```bash
mkdir -p ~/.config/tmux-ai
cp examples/config.yaml ~/.config/tmux-ai/config.yaml
```

可設定 `model`、`host`、`max_iterations`、`timeout`、`verbose`。優先序為：
`CLI flag` > `環境變數`（模型與 host）> `使用者設定檔` > `內建預設`。工作流的
角色、prompt 與 handoff 設定仍應放在各自的 preset。

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

### `pingpong` — 兩個 Agent 交替執行

```
tmux-ai pingpong [flags]
```

協調兩個 pane 裡的 AI agent（例如 Codex 和 Claude Code）：送初始指令到起始 pane，等待完成後捕捉輸出，再 relay 到下一個 pane，循環直到偵測到停止關鍵字。預設採固定交替；加上 `--coordinator` 後，每輪由 LLM 根據最新結果決定下一個 pane 與交接指令。

**由誰開始？** 預設 `--start auto`，由本地 LLM 根據 `--initial` 指令和兩個 pane 的 role 描述自動判斷。也可用 `--start pane1` 或 `--start pane2` 手動指定。

> 直接透過 tmux `send-keys` / `capture-pane` 與 pane 互動，適合任何互動式 CLI agent。`--start auto`、`--coordinator` 和 `--summarize` 會使用設定的 Ollama LLM。

#### 完成與停止偵測

pingpong 靠兩個標記運作：

| 標記 | Flag | 用途 |
|------|------|------|
| Done marker | `--done`（預設 `DONE`） | agent 完成單回合時輸出的字串，偵測到就切換到下一個 pane |
| Stop keyword | `--stop`（預設 `ALL_DONE`） | 無 coordinator 時直接停止；啟用 coordinator 時視為停止請求，須通過 policy |

> **重要：** pingpong 會在每輪基礎標記後加上 run/round token，例如 `DONE__TMUXAI_abc_2`，並在訊息末尾要求 agent 輸出該輪的完整控制標記。這可避免舊對話、bootstrap 回覆或 prompt 中的 `DONE` 被誤判成本輪完成。CLI 仍以 `--done DONE`、`--stop ALL_DONE` 設定基礎名稱。

> Status receipt 採 frame 形式：`HANDOFF_STATUS_BEGIN__…` 與 `HANDOFF_STATUS_END__…` 各自獨佔一行；兩者之間的 JSON 必須是 compact 的單一實體行，且 JSON 字串內不得有未跳脫的換行（需要換行時寫 `\\n`）。`summary` 只放簡短控制摘要，詳細結果放 artifact，避免 TUI 強制換行。JSON 為 `{"version":1,"outcome":…,"summary":…,"tests":[],"prompt_head":…}`；`prompt_head` 由 runner 驗證，見下方送達檢查。runner 對畫面捕捉到的 JSON 字串內硬換行會做受限復原，但 outbox receipt 始終採嚴格 JSON 驗證。舊的單行寫法仍然接受，runner 也以單行形式合成自己產生的 receipt。畫面上出現多個 frame 時取最新的一個。
>
> file preset 可設 `completion_mode: helper`。runner 會在 outbox 建立 task manifest，agent 完成工作後只需在其 TUI 內執行 prompt 提供的 `tmux-ai handoff complete --task … --prompt-head … --outcome … --summary …`；helper 會原子發布 receipt，並印出相同的 status frame 與 completion marker。helper 的 receipt 已通過 schema 和 prompt-head 驗證後，非 `/goal` round 會直接交接，不必等待畫面 marker 或 `--receipt-idle-timeout`。`manual` 仍是預設，保留既有手工 receipt 相容性。
>
> file transport 的 receipt repair 失敗，或 round timeout 明確顯示「畫面已連續…沒有非 chrome 變化」但尚未出現 completion marker 時，互動提示可輸入 `l`。runner 只讀取目前 pane 畫面，讓 LLM 明確判定 `completed`、`still_working` 或 `needs_human`，再由 runner 在本地驗證替代 receipt；這不會重送原任務或傳送訊息給 agent。receipt repair 仍會驗證原本的 `prompt_head`；靜止畫面只能在 LLM 明確判定完成時建立強制 `needs_followup` receipt，交由 coordinator 複核，絕不直接停止 workflow。`still_working`、`needs_human` 或證據不足會拒絕交接。`y` 仍只會繼續等待。
>
> Round 失敗提示也可輸入 `r` 重送本輪原始 prompt。這是明確的人工覆寫：agent 可能因此再次執行工作，請只在確認 prompt 沒有送達或沒有被處理時使用。runner 每輪最多重送兩次，保留 relay byte budget，並改用新的送達 marker 重新確認 paste，而不是把畫面上既有的舊 marker 誤判為新送達。
>
> 等待 round 時按一次 `Ctrl-C` 不會取消 workflow，而是立即打開目前 round 的 `[y/l/r/N]` 選單；選單顯示期間再按一次 `Ctrl-C` 才會真正離開。若要由另一個 terminal 觸發同一個選單，可執行 `kill -USR1 <tmux-ai PID>`。`SIGTERM` 仍會立即取消。
>
> 若 agent 已輸出該輪有效的 status receipt 卻遺漏 `DONE`，`--receipt-idle-timeout`（預設 30 秒）會在畫面維持不變後放行交接。這只適用於 nonce 相符且通過 schema 驗證的 receipt；設為 `0` 可停用此 fallback。
>
> 這裡的「不變」不是整張 capture 逐位元組相同。coding-agent TUI 會永遠重繪計時器、token 計數與 footer 提示，這樣的畫面永遠不會相同。runner 改為量測每一列的變動頻率：最近 32 次比較中變動 3 次以上的列視為 UI chrome，判斷閒置時忽略。列以「距離最後一行的距離」定位，因為輪次進行中 capture 會變長，只有這個距離對 footer 穩定，而 alternate-screen TUI 高度固定，頂端狀態列同樣適用。三道保險：樣本不足 8 次前不做任何判斷；被視為 chrome 的列超過畫面四分之一時，代表畫面真的在忙，退回整張比較；帶本輪 marker 的列一律不排除。
>
> 頻率無法區分 spinner 與「Analyzing 37/512」這類原地更新的進度列，兩者都會被當成 chrome。`--screen-idle-timeout` 的 LLM selector 沒有 receipt 可對照，預設仍比較整張 capture；要一併套用需明確加上 `--screen-idle-normalize`（preset：`screen_idle_normalize`）。implement/review 的 file preset 已設為 60 秒且啟用此濾波：LLM 判定完成時，runner 只會建立 `needs_followup` receipt 交給 coordinator，判定仍在工作或需要人工時繼續等待；它不會直接 stop。
>
> Prompt 以 `--paste-chunk-size` 個 rune 為單位貼入，每塊之間等待 `--paste-chunk-delay` 毫秒（preset：`paste_chunk_size`、`paste_chunk_delay_ms`）。預設 500 runes／1000 毫秒偏保守：一次收到大量貼上的互動式 TUI 可能吃掉部分內容或提前送出。但在這個速率下，32 KB 的 relay 要花一分鐘以上才貼完，而那整段時間都是目的端可能誤處理輸入的窗口。各 agent 的耐受度差很多，確認目的端能處理大塊貼上時可放寬（例如 `--paste-chunk-size 4000 --paste-chunk-delay 200`）。
>
> 送出前會先問 tmux 這個 pane 是否真的收得到輸入（`#{pane_in_mode}`、`#{pane_dead}`、`#{pane_current_command}`）。pane 在 copy-mode 時，`send-keys` 與 `paste-buffer` 都會被導進該模式的 key table，整段 prompt 直接被 tmux 丟掉——不是排隊、不是延後送達（已在真實 tmux 驗證）。使用者用滑鼠滾輪捲動 pane 就會進入 copy-mode，這是整輪 prompt 憑空消失最常見的成因，而且事後在 pane 上留不下任何痕跡，所以檢查必須發生在貼上之前：runner 會先送 `send-keys -X cancel` 取消該模式並確認，再貼上。pane 程序已結束（`pane_dead=1`）直接停止。前景程序是 shell 時也停止，因為 agent 不在等待輸入，prompt 會被 shell 當成指令逐行執行；確定要這樣做時用 `--allow-shell-pane`。無法讀取 pane 狀態時不阻擋送出——讀不到狀態不是 pane 有問題的證據。
>
> 每則 runner 產生的 prompt 前後各夾一個標記，讓送達可以從兩端檢查。最後一行是本輪唯一的送達標記 `TMUXAI_PROMPT_ECHO__…`：貼上後、按 Enter 前，runner 會在畫面上找它。找到就證明「這一輪的 paste」真的抵達輸入區（不是上一輪的殘影，因為標記逐輪不同）。找不到時分兩種：畫面出現 `[Pasted text …]` 這類佔位符，或畫面比貼上前多出兩列以上，代表確實收到了只是尾端沒渲染，只印一次警告後繼續；完全看不出收到任何東西時直接停止，**不按 Enter**——對一個沒收到我們文字的輸入區按 Enter，只會提交裡面原本殘留的東西。`--input-echo-check=strict` 會把「無法確認」也視為失敗；`off` 完全停用。會讀輸入但完全不回顯的目的端（例如自訂的非互動 wrapper）在畫面上與「沒收到」無法區分，必須用 `off`。
>
> 另一端是 prompt 最開頭的 `TMUXAI_PROMPT_HEAD__… <nonce>`，nonce 每輪隨機且只出現在那一行，無法從其他 marker 推導。控制協定要求 receipt 帶上 `"prompt_head"`，填入該值。這補上 receipt 原本證明不了的事：receipt 位於訊息尾端，能收到它只證明 agent 看到「結尾」。若 prompt 被截斷、或在分塊貼上途中被提前提交成兩則訊息（agent 只對後半段作答），它就拿不到 head nonce，必須回報 `"missing"`，runner 隨即以「只有後半段送達」停止，且不會被 text frame 還原、outbox receipt 或畫面 selector 這些復原路徑救回來——那些工作是對半個 prompt 做的。填錯值（例如答的是別輪的 prompt）同樣停止。receipt repair 是唯一例外：那一輪的原始 prompt 早已完成，不是送達測試，因此 runner 直接把 nonce 寫在修補指令裡，只為維持單一 receipt schema。
>
> Enter 送出後的確認判準不是「畫面有變」。agent TUI 會永遠重繪計時器與 footer 提示，所以即使 Enter 被吃掉，畫面每次 poll 都不一樣，「有變」永遠成立。runner 改為只看「非重繪列」是否變化：prompt 被接受時輸入區會清空、內容會移進 transcript，這些列在那之後就不再變動；Enter 被吃掉時只有 footer 在動。一列必須被觀察到重繪過才能認定是重繪列，因此判斷需要時間：`--submit-confirm-timeout`（預設 8000 毫秒）內若有 5 列以上同時變化，視為輸入區已被消化並立即通過（正常送出通常在 1 秒內就走這條路）；否則等觀察滿 4 秒後才以非重繪列作結論。窗口小於約 6000 毫秒時無法分辨 chrome，判準會退回舊行為「有任何變化就算送出成功」。判定 Enter 未生效時會依 `--submit-retries` 重送 Enter，仍無效則停止並說明看到的是什麼。
>
> 送出確認只有幾秒的視野，因此每輪還有一道長視野檢查。runner 記下「prompt 還在輸入區、尚未按 Enter」時的畫面，輪次進行中持續比對：若畫面除了重繪列以外和當時完全相同，代表這一輪什麼都沒發生過——不是 agent 在慢慢想，而是 prompt 沒進去。超過 `--pending-input-timeout`（預設 45 秒）會補送 Enter，次數上限 `--pending-input-nudges`（預設 1），用完仍相同則以明確錯誤停止，而不是等到 `--round-timeout` 才報「未輸出完成標記」。補送 Enter 之所以安全，正是因為觸發條件是「畫面與按 Enter 前完全相同」：任何權限確認對話框本身就是非重繪列的變化，會讓這道檢查先退場。這段期間也不會呼叫 `--screen-idle-timeout` 的 LLM selector——那張畫面靜止的原因就是從來沒送進去，問 LLM「這輪完成了嗎」只會得到錯的答案。此檢查比對的是 pane 目前顯示的畫面（不含 scrollback），因為輸入區是畫面的性質；設 `--pending-input-timeout 0` 可停用。
>
> 輪次逾時的錯誤訊息會指出實際卡在哪一段：完全沒有完成標記、有完成標記但 receipt 無效（附上驗證錯誤）、text handoff frame 沒有關閉、有有效 receipt 但缺完成標記、envelope 反覆被改寫而無法穩定、或 goal 尚未關閉。完成標記只在 pane output stream 出現時也會註明。
>
> `--pane-stream`（preset：`pane_stream: true`，預設關閉）另外用 `tmux pipe-pane` 把每個 pane 的輸出鏡像到本機檔案。`capture-pane` 只看得到目前放得下、且 TUI 最後畫出來的內容，因此 alternate screen 捲掉的完成、或兩次 poll 之間被重繪蓋掉的完成都會遺失；輸出串流兩者都留得住，而且因為鏡像發生在 tmux 端，agent 在容器或 ssh 遠端也一樣有效。串流只會餵給和畫面同一套 nonce-bound envelope 驗證，因此它能救回「確實顯示過」的完成，不能無中生有；只看到 marker 卻湊不出有效 envelope 時會讓路給其他 fallback。已經被別人 pipe 的 pane 不會被接管，該 pane 就退回純畫面偵測。
>
> 正常換手採 runner 端兩階段驗證，不要求 agent 額外回覆 ACK：先驗證同一輪的完整 text/file envelope、status receipt 與完成 marker，再等待該 envelope 於 `--completion-settle-timeout` 期間保持一致。穩定判準是已驗證的 envelope 而非整張畫面，因此 TUI 的計時器、token 計數或 footer 重繪不會延後已完成的交接。預設為 1000 毫秒；設為 `0` 可停用穩定期。preset 可用 `completion_settle_ms` 設定。

> 啟用 `--coordinator` 時，任何 member 的 stop marker 都只是候選停止訊號。Coordinator 會連同 `--coordinator-policy`、目前 member 與最新結果一起判斷；因此可以規定「只有 reviewer 驗證通過才能停止」，並用 `--stop-members pane1` 做程式層的核准者限制。

#### 起始 Pane 選擇

`--start` 決定哪個 pane 先執行初始指令：

| 值 | 行為 |
|------|------|
| `auto`（預設） | LLM 根據 `--initial` + `--pane1-role` / `--pane2-role` 判斷最適合的起始 pane |
| `pane1` | 固定由 pane1 開始 |
| `pane2` | 固定由 pane2 開始 |
| preset `routing.mode: state_machine` | 由 `routing.initial_member` 固定開始，不呼叫 LLM |

`auto` 模式下，LLM 會收到初始指令和兩個角色描述，回傳 JSON `{"start": 1 或 2, "reason": "理由"}`。若未設定任何 role 或 LLM 無法判斷，預設由 pane1 開始。

固定 reviewer/fixer 閉環可用 preset 的 state machine，不需 coordinator：

```yaml
routing:
  mode: state_machine
  initial_member: pane1
  transitions:
    pane1:
      success: {action: stop}
      needs_followup: {action: send, target: pane2}
      blocked: {action: needs_human}
```

每位 member 都必須定義 `success`、`needs_followup`、`blocked`；`stop` 仍受 `stop_members` 限制。state machine 只使用已驗證 receipt outcome，不會判讀 agent 自由文字，也不會呼叫 coordinator。

> 例如 `--initial "修復 main.go 的 bug"` 搭配 pane2 角色「工程師，負責修改程式碼」，LLM 可能判斷直接由 pane2 開始，跳過 pane1 的規劃階段。

使用 YAML preset 且採 `--start auto` 時，應提供中性的 `initial` 欄位。`initial_pane1` / `initial_pane2` 只用於明確指定起始 pane；若兩者不同又缺少 `initial`，程式會停止並要求補上，不再誤用 pane2 指令。

#### 分工模式

透過四個 flag 實現角色分工：

| Flag | 說明 |
|------|------|
| `--pane1-role` | 每次傳給 pane1 的訊息前面加上這段角色設定 |
| `--pane2-role` | 每次傳給 pane2 的訊息前面加上這段角色設定 |
| `--pane1-goal-mode` | 對 pane1 的每個一般任務 packet 前加上 `/goal ` |
| `--pane2-goal-mode` | 對 pane2 的每個一般任務 packet 前加上 `/goal ` |
| `--to-pane1-prompt` | relay **到 pane1** 時的指令（例如 review / 規劃） |
| `--to-pane2-prompt` | relay **到 pane2** 時的指令（例如實作 / 修改） |

未指定 prompt 時使用預設值「請根據以下上一步的結果繼續執行。」。

`--pane1-goal-mode` 與 `--pane2-goal-mode` 可分別啟用，適合只有部分 agent 支援 `/goal` 的情況。前綴會套用到該 pane 的初始任務與每次 relay；runner 的 receipt-repair 控制訊息不會加上此前綴。若目標 agent 能可靠回顯 runner 的 nonce marker，可加上 `--strict-goal-completion-marker`，要求 `GOAL_DONE__TMUXAI_...` 出現在 `DONE`／`ALL_DONE` 前；預設關閉，以相容使用原生 `/goal` 完成狀態但不回顯自訂 marker 的 agent。

#### LLM Coordinator

啟用 `--coordinator` 後，每一輪完成時，LLM 會收到原始目標、目前輪次、兩個 pane 的角色與預設方向，以及剛完成的完整輸出，並回傳：

```json
{"action":"send","target":"pane2","instruction":"依 review 報告修正競態並執行 go test ./...","reason":"最新輸出是待修正的 review 結果","confidence":0.92}
```

`target` 必須是 `pane1` 或 `pane2`，也可以再次選擇剛完成工作的 pane。Coordinator 只負責控制層：依可信的 workflow policy 判斷路由、具體指令、停止或人工介入，不會取得 tmux 或檔案工具權限。可用 `--coordinator-policy` 或 preset 的 `coordinator_policy` 定義 reviewer-only approval 等規則；模型逾時、解析失敗或低信心時會安全停止，不會回退成未審核的固定交替。

#### 從既有 Pane 回覆開始

若 pane1 在啟動 `pingpong` 前已完成規劃、分析、review、測試或其他工作，可用 `--initial-from pane1` 將它的最新完成回覆作為初始 context：

```bash
tmux-ai pingpong \
  --pane1 trec:1 \
  --pane2 trec:2 \
  --start pane2 \
  --initial-from pane1 \
  --initial-from-lines 500 \
  --initial "依 reviewer 最新回覆修正問題並補測試" \
  --coordinator \
  --model minimax-m3:cloud
```

`pingpong` 會精確保留最後 N 行，加入由舊到新的行號，讓 LLM 只選擇最新完成回覆的起訖行號；它不假設回覆具有特定標題、格式或任務類型。實際交接內容由程式從原始終端文字逐字切片，因此不會由 LLM 重寫或遺失項目。若選擇結果無效，流程會停止，不會把整段舊對話交給下一個 pane。

#### Flags

| Flag | 預設 | 說明 |
|------|------|------|
| `--pane1` | — | 第一個 pane（alias / `%ID` / `session:window`） |
| `--pane2` | — | 第二個 pane（alias / `%ID` / `session:window`） |
| `--initial` | — | 初始指令（必填） |
| `--start` | `auto` | 起始 pane：`auto`（LLM 判斷）、`pane1`、`pane2` |
| `--stop` | `ALL_DONE` | 偵測到此關鍵字時結束整個循環 |
| `--done` | `DONE` | agent 完成單回合時輸出的標記 |
| `--pane1-goal-mode` | `false` | 對 pane1 的一般任務前加上 `/goal ` |
| `--pane2-goal-mode` | `false` | 對 pane2 的一般任務前加上 `/goal ` |
| `--strict-goal-completion-marker` | `false` | 只有 agent 能可靠輸出 runner 的 `GOAL_DONE__...` marker 時才啟用；要求該 marker 先於完成 marker。 |
| `--max-rounds` | `20` | 最大交替輪數（安全上限） |
| `--round-timeout` | `600` | 每輪超時秒數（0 = 不限制） |
| `--completion-settle-timeout` | `1000` | 完整且有效的完成 envelope 必須維持不變多久才換手（毫秒；0 停用）。此驗證完全在 runner 本地進行。 |
| `--receipt-idle-timeout` | `30` | 有效本輪 status receipt 出現後，畫面保持不變多久即可交接（秒；0 停用）。 |
| `--paste-chunk-size` | `500` | 每次貼進 pane 的 rune 數。 |
| `--paste-chunk-delay` | `1000` | 兩個貼上區塊之間的間隔（毫秒；0 表示不等待）。 |
| `--screen-idle-normalize` | `false` | 讓 `--screen-idle-timeout` 也忽略持續重繪的 UI chrome（receipt-idle 一律啟用）。 |
| `--pane-stream` | `false` | 以 `tmux pipe-pane` 鏡像 pane 輸出，救回畫面已捲掉或被重繪蓋掉的完成 envelope。 |
| `--screen-idle-timeout` | `0` | 畫面持續不變後，讓 LLM 從全螢幕判定是否已有完整回覆（秒；0 停用）。text transport 會選取可 relay 的回覆；file transport 只在判定完成時建立非終止的 `needs_followup` receipt 交給 coordinator。 |
| `--interval` | `2000` | 輪詢間隔（毫秒） |
| `--lines` | `80` | 每次捕捉的 pane 行數 |
| `--summarize` | `false` | relay 前先用 Ollama 摘要內容，減少 context |
| `--coordinator` | `false` | 每輪由 LLM 決定下一個 pane 與交接指令 |
| `--coordinator-policy` | — | 可信的 workflow 控制規則；決定角色路由與停止條件 |
| `--stop-members` | — | 可核准 coordinator stop 的 member ID allowlist，例如 `pane1` |
| `--coordinator-timeout` | `60` | 每次 coordinator 決策的 timeout 秒數；`0` 使用全域 timeout |
| `--max-selection-retries` | `2` | 每輪允許 coordinator 要求重新擷取及選擇回覆的次數 |
| `--initial-from` | — | 從 `pane1` 或 `pane2` 的最新完成回覆 bootstrap |
| `--initial-from-lines` | `500` | bootstrap 時供 LLM 選擇的精確尾端行數 |
| `--submit-delay` | `500` | 貼上訊息後等待多少毫秒再送出 Enter；大型 prompt 可提高 |
| `--submit-confirm-timeout` | `8000` | Enter 送出後觀察多少毫秒才判定 prompt 是否被輸入區消化（0 停用；低於約 6000 會退回「有任何變化就算成功」） |
| `--submit-retries` | `1` | 判定 Enter 未生效時額外重送 Enter 的次數 |
| `--allow-shell-pane` | `false` | 即使 pane 前景程序是 shell（agent 不在執行中）也送出 prompt |
| `--input-echo-check` | `auto` | 按 Enter 前確認貼上的 prompt 已出現在畫面：`auto` 只在完全看不出收到內容時停止；`strict` 連「無法確認」也停止；`off` 停用（回顯不可見的目的端需要） |
| `--pending-input-timeout` | `45` | prompt 留在輸入區未被消化多少秒後補送 Enter（秒；0 停用） |
| `--pending-input-nudges` | `1` | 一輪內最多補送幾次 Enter；用完仍未被消化就停止 |
| `--max-relay-bytes` | `32768` | 任一送出訊息的 UTF-8 byte 上限；超限時停止且不送出 |
| `--max-total-relay-bytes` | `131072` | 整個 workflow 累積送出 byte 上限 |
| `--max-llm-calls` | `40` | selector、coordinator 與 summarizer 的呼叫總上限 |
| `--max-same-pane-rounds` | `3` | coordinator 可連續交給同一 pane 的次數上限 |
| `--max-duplicate-handoffs` | `2` | 相同目標、指令與實質內容可重送的次數上限 |
| `--min-response-lines` | `1` | 可交接回覆的最少實質行數 |
| `--max-response-lines` | `200` | 可交接回覆的最多實質行數 |
| `--min-coordinator-confidence` | `0.7` | coordinator 核准 `send` 所需的最低信心 |
| `--approval-threshold-bytes` | `16384` | 超過此大小需明確加上 `--allow-large-relay`；`0` 停用 |
| `--allow-large-relay` | `false` | 人工檢查後允許超過 approval threshold 的訊息 |
| `--dry-run` | `false` | 顯示初始交接內容與大小後停止，不操作 pane |
| `--new` | `false` | 開始 workflow 前，依序向兩個 pane 送出並提交 `/new`，清除既有對話後才送出初始任務 |
| `--compact` | `false` | 啟用安全的 Codex `/compact` 控制流程 |
| `--compact-mode` | `before_handoff` | `before_handoff` 依輪次壓縮；`threshold` 依 context meter 壓縮（使用 `llm` 時應選此模式） |
| `--compact-min-rounds` | `3` | 同一 member 兩次 `/compact` 的最少完成輪數 |
| `--compact-at-percent` | `70` | threshold mode 的 context 使用率門檻 |
| `--compact-members` | 全部 Codex pane | 可執行 `/compact` 的 member ID allowlist |
| `--context-meter` | `off` | `off`、`screen_regex`、`estimate`、`llm` 或 `api_usage` |
| `--context-meter-bottom-lines` | `16`（llm） | 提供給 LLM context meter 的 pane 底部行數（3–100） |
| `--context-meter-llm-min-confidence` | `0.85`（llm） | LLM 判定送出 `/compact` 所需的最低信心 |
| `--pane1-role` | — | pane1 的角色描述，附加在每則訊息前 |
| `--pane2-role` | — | pane2 的角色描述，附加在每則訊息前 |
| `--to-pane1-prompt` | — | relay 到 pane1 時的指令 |
| `--to-pane2-prompt` | — | relay 到 pane2 時的指令 |

啟用 `--coordinator` 時，LLM 是送出前的安全閘門。它會先看到 selector 選出的實際候選內容、bytes、行數及目前 workflow 使用量，並回傳 `send`、`retry_selection`、`stop` 或 `needs_human`。只有 `send` 且 confidence 達標才會操作 tmux；解析失敗或其他 action 都不會 fallback 到自動交替。

`retry_selection` 會立即重新擷取目前 pane，將 coordinator 的理由交給 response selector 後再做一次 coordinator 決策；超過 `--max-selection-retries` 才停止。`needs_human` 會清楚中止並輸出需要人工處理的原因。`stop` 只有在 workflow policy 允許時才應由 coordinator 回傳。

`a.py`、`b.py` 可用同名大寫環境變數調整主要保護，例如 `MAX_TOTAL_RELAY_BYTES`、`MAX_LLM_CALLS`、`MAX_SAME_PANE_ROUNDS`、`MAX_DUPLICATE_HANDOFFS`、`MIN_RESPONSE_LINES`、`MAX_RESPONSE_LINES`、`MIN_COORDINATOR_CONFIDENCE` 與 `APPROVAL_THRESHOLD_BYTES`。人工確認大訊息後可設 `ALLOW_LARGE_RELAY=1`；只預覽可設 `DRY_RUN=1`。

收到 Ctrl-C 或 SIGTERM 時，pending handoff 會立即取消。若尚未貼上，tmux 不會收到任何文字；若正在貼上後的 submit delay，計時器會取消且不會再送 Enter，避免停止後才延遲提交 agent 任務。

#### 訊息結構

每次 relay 時，目標 pane 收到的完整訊息：

```
TMUXAI_PROMPT_HEAD__TMUXAI_<token>_<round> <nonce>
                          ← runner 產生；receipt 的 prompt_head 必須回報這個值

[pane-role]               ← --pane1-role 或 --pane2-role

[relay instruction]       ← --to-pane1-prompt 或 --to-pane2-prompt

=== 上一步輸出 ===
[captured content]
=== 輸出結束 ===

TMUX-AI runner control protocol：text/file 資料平面、status receipt frame、
本輪完成 marker 與 prompt_head 要求（runner 產生，preset 不需自行撰寫）

TMUXAI_PROMPT_ECHO__TMUXAI_<token>_<round>
                          ← runner 的送達檢查標記；不需回應，也不會被 relay
```

頭尾兩行都由 runner 產生並自行驗證：開頭那行讓 receipt 能證明整則 prompt 完整
送達，最後一行讓 runner 在按 Enter 前確認 paste 真的進了輸入區。

#### `review.md` review/fix preset

`presets/review-md-fix.yaml` 將 reviewer/fixer 角色、prompt、text handoff、
coordinator policy、停止權限與 timeout 集中管理。以下指令固定從 `trec:1`
開始 review；有問題時交給 `trec:2` 依 `./review.md` 修正，再回到 reviewer
複查，只有 reviewer 能核准完成：

```bash
tmux-ai pingpong \
  --preset ./presets/review-md-fix.yaml \
  --pane1 trec:1 \
  --pane2 trec:2 \
  --start pane1
```

此 preset 也啟用 45 秒的 `screen_idle_timeout`：完成標記仍是優先、零 LLM
成本的路徑；若某個 coding agent 完全不顯示標記，畫面穩定後 selector 才會
從原始畫面挑選已完成回覆。selector 無法可靠確認完成時不會交接；即使 fallback
成功，runner 也一律標為 `needs_followup`，不能直接觸發 stop。

#### 範例

```bash
# auto 模式：LLM 根據 role 自動判斷誰先開始
tmux-ai pingpong \
  --pane1 codex --pane2 claude \
  --initial "修復 main.go 裡的 memory leak" \
  --pane1-role "你是架構師，負責分析問題和擬定修復計畫。" \
  --pane2-role "你是工程師，負責根據計畫修改程式碼。"
# → LLM 可能判斷由 pane2 (工程師) 直接開始

# 基本交替：兩個 agent 互相 review（無 role，預設 pane1 先）
tmux-ai pingpong \
  --pane1 codex-pane --pane2 claude-pane \
  --initial "分析 main.go 並提出重構建議" \
  --stop "ALL_DONE" --done "DONE"

# 指定 pane2 先開始
tmux-ai pingpong \
  --pane1 %5 --pane2 %7 \
  --initial "直接修復 bug" --start pane2

# Codex 負責規劃，Claude Code 負責實作
tmux-ai pingpong \
  --pane1 codex --pane2 claude \
  --initial "分析專案結構，擬定第一階段重構計畫" \
  \
  --pane1-role "你是架構師，負責擬定計畫和 review 結果。不要直接修改程式碼。" \
  --pane2-role "你是工程師，負責根據計畫實際修改程式碼。修改後請執行測試確認。" \
  \
  --to-pane1-prompt "請 review 以下修改結果，確認是否正確，並擬定下一步計畫。如果所有工作已完成，請輸出 ALL_DONE。" \
  --to-pane2-prompt "請根據以下計畫執行程式碼修改，完成後執行測試。" \
  \
  --coordinator \
  --max-rounds 10 \
  --round-timeout 600 \
  --summarize

# 用 pane ID，每輪最多 5 分鐘
tmux-ai pingpong \
  --pane1 %5 --pane2 %7 \
  --initial "review the codebase architecture" \
  --round-timeout 300
```

> **運作流程（`--start auto --coordinator`）：**
> 1. LLM 分析 `--initial` + role → 決定起始 pane
> 2. 初始指令 → 起始 pane（例如 codex 規劃）
> 3. 等待 `DONE` → 捕捉輸出 → coordinator 決定下一個 pane 與具體指令
> 4. 目標 pane 完成後，coordinator 根據新結果再次決策
> 5. 重複直到 coordinator 依 policy 核准停止、無 coordinator 時任一 pane 輸出 `ALL_DONE`，或達到 `--max-rounds` 上限

### `coordinate` — 多 Window 角色路由

`coordinate` 將 `pingpong` 的交接流程擴充到任意數量的 tmux window/pane。每個成員在 YAML preset 中預先定義名稱、target、角色與預設交接方向；coordinator 會先選擇最適合處理初始目標的成員，之後依每輪最新結果選擇下一個成員並產生具體指令。

```bash
tmux-ai coordinate \
  --preset presets/multi-window-team.yaml \
  --initial "修正目前最重要的 correctness 問題並完成獨立驗證" \
  --model qwen2.5:14b
```

Preset 格式：

```yaml
initial: 檢查目前工作樹，完成修正與驗證
coordinator_policy: |-
  implementer 完成後必須交給 tester；只有驗證證據充分時才能 stop。
stop_members:
  - architect
max_selection_retries: 2
members:
  - name: architect
    target: dev:architecture
    role: 你是架構師，只負責分析與 review，不修改檔案。
    handoff_prompt: 分析最新結果並決定下一階段。
  - name: implementer
    target: dev:implementation
    role: 你是實作工程師，負責修改程式碼與補測試。
    goal_mode: true # 每次任務開頭加上 /goal ，只影響這個 target
  - name: tester
    target: dev:verification
    role: 你是獨立驗證工程師，不修改產品程式碼。
```

`target` 支援 alias、`%pane_id`、`session:window` 與 `session:window.pane`。`name`、`target`、`role` 必填且必須唯一；`handoff_prompt` 選填。若該 pane 的 agent 支援 `/goal` 指令，可設 `goal_mode: true`：每一個一般任務 packet 都會以 `/goal ` 開頭，只有該 member 受影響。runner 的 receipt-repair 控制訊息不會加上此前綴。角色會附加到每次送往該成員的訊息，coordinator 的 `target` 只能從 preset 的 member name 中選擇。

`coordinator_policy` 是送給 coordinator 的可信控制規則，可限制必經驗證順序與何時應 `needs_human`；`stop_members` 則是程式硬性執行的最終核准者 allowlist，未授權 member 的 `stop` 會以 policy violation 中止。`max_selection_retries` 預設為 2。完整工作內容仍可透過共享檔案交換，但 coordinator 只根據 terminal status 與 policy 做控制判斷。

若需從全新對話開始，可在任一模式加上 `--new`。runner 會依 preset 成員順序（pingpong 則 pane1、pane2）先向每個 participating pane 送出並提交 `/new`，再送出第一個任務；`--dry-run` 只顯示預計動作，不會送出 `/new`。在 YAML preset 也可設定 `new_session: true`，而命令列 `--new` 可明確啟用。

`coordinate` 與 `pingpong` 共用同一個 handoff engine、coordinator prompt/JSON schema 與 action mapping，因此完成標記、兩階段 envelope 驗證、policy stop gate、response reselection、bytes/LLM call 限制、重複交接防護、送出確認及 Ctrl-C 取消行為一致。主要安全 flags 與 `pingpong` 相同；`--completion-settle-timeout` 也可由 coordinate preset 的 `completion_settle_ms` 設定。可先加 `--dry-run` 檢查第一個 coordinator 決策與實際 prompt，而不操作 tmux。

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
### Ping-Pong 分工協作

```bash
# Codex 負責規劃，Claude Code 負責實作，交替直到完成
# --start auto 讓 LLM 根據任務和角色自動決定誰先開始
tmux-ai pingpong \
  --pane1 codex --pane2 claude \
  --initial "分析專案結構，擬定第一階段重構計畫" \
  --start auto \
  --pane1-role "你是架構師，負責擬定計畫和 review 結果。不要直接修改程式碼。" \
  --pane2-role "你是工程師，負責根據計畫實際修改程式碼。修改後請執行測試確認。" \
  --to-pane1-prompt "請 review 以下修改結果，確認是否正確，並擬定下一步計畫。如果所有工作已完成，請輸出 ALL_DONE。" \
  --to-pane2-prompt "請根據以下計畫執行程式碼修改，完成後執行測試。" \
  --coordinator \
  --max-rounds 10 --round-timeout 600 --summarize
```

> 運作流程：LLM 判斷起始 pane；之後 coordinator 每輪根據最新結果與 policy 選擇下一個 pane，member 的 `ALL_DONE` 也必須經 coordinator 核准才會停止。


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

所有功能送往 tmux pane 的文字（包含 `ask`、workflow launch、watch、agent tools 與 `pingpong`）都使用相同節流器：依 Unicode 字元切割，每批最多 500 個字元，每批送出後等待 1 秒才繼續下一批或送 Enter。允許文字混合 Ctrl-C 等控制碼；程式會保持原始順序，控制碼立即送出，前後的文字仍各自節流。收到取消訊號時，等待會立刻結束，尚未送出的批次不會再傳送。
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


---

## Handoff transport

`pingpong` 與 `coordinate` 以 runner 管理 agent 間資料交接。預設為 `text`，適合不同主機、SSH 或 container 中的 panes；runner 會要求每輪輸出 tokenized text frame，並將其中內容原文送入下一個 pane。text payload 受 `--max-text-handoff-bytes`（預設 32768）與 `--max-text-handoff-lines`（預設 200）限制，且 `--lines` 必須保留足夠 capture history。

同一台主機或共享 filesystem 可使用 `--handoff-transport file`。runner 為每輪建立私有 outbox，完成後只封存 regular files、配置 immutable artifact ID 與 SHA-256，再把經驗證的唯讀路徑提供下一位 member。不要在 preset 或 prompt 中指定共同檔名；producer 將檔案放入 runner-provided outbox，consumer 只讀 runner-provided incoming artifacts。

Preset 可指定：

```yaml
handoff_transport: file
artifacts:
  forward: produced_or_incoming
  max_files_per_turn: 8
  max_file_bytes: 1048576
  max_turn_bytes: 4194304
  max_run_files: 64
  max_run_bytes: 33554432
  keep: false
text_handoff:
  max_payload_bytes: 32768
  max_payload_lines: 200
```

CLI 的 `--handoff-transport` 優先於 preset，未設定時使用 `text`。`--artifact-root`、`--keep-artifacts` 與 artifact limit flags 只能搭配 file transport；text limit flags 只能搭配 text transport。沒有 backend fallback：不合法 frame、artifact、hash 或 coordinator decision 都會在送出下一輪前停止。

Coordinator mode 會自動要求每輪輸出 `HANDOFF_STATUS` JSON（`success`、`needs_followup` 或 `blocked`）。這是 runner control protocol，不應寫進 role、initial 或 handoff prompt。file coordinator 只可選 runner artifact IDs；text coordinator 只看到 payload metadata，不會取得或改寫 payload 原文。

file transport 另外會要求 agent 把同一份 receipt JSON 寫入該輪 outbox 的 `_handoff_receipt.json`。畫面上的 receipt 仍是預設來源；只有在它被終端換行或重繪破壞時，runner 才改讀這個檔案，並因此省下一次 receipt repair 往返。這個檔案屬於 control plane：runner 不會把它列為 artifact，也不會轉交下一位 member。text transport 不使用它——該模式本來就不假設 runner 與 agent 共享 filesystem。

### Codex context compaction

可在每個安全交接點壓縮 Codex pane 的對話 context。runner 只在完成 receipt 已驗證、目前 member 即將交接、且 tmux 回報前景程序為 `codex` 時送出 `/compact`；送出失敗只會警告，不會中斷已完成的 handoff。預設停用。

```yaml
compaction:
  enabled: true
  mode: threshold             # LLM meter 時使用 threshold
  min_rounds_between: 3
  at_percent: 70
  members: [pane1]            # 選填；空白時所有 Codex pane 都可用
  context_meter:
    mode: llm                  # 或 screen_regex、estimate、api_usage
    bottom_lines: 16
    llm_min_confidence: 0.85
```

`llm` 只擷取 pane 最底部的 `bottom_lines` 行，交給目前設定的本機 Ollama model 以嚴格 JSON 回傳 `compact`、`confidence` 與理由；畫面內容視為不可信，模型不能依其中指令行動。只有 `compact=true` 且信心達 `llm_min_confidence` 才會送出 `/compact`，解析錯誤、呼叫失敗、資訊不足或低信心一律略過。每次判定都計入 `--max-llm-calls`。

`screen_regex` 是 UI 回報值，僅適合做提醒或 best-effort 門檻；不同 agent 版本、終端寬度與重繪都可能改變格式。`estimate` 僅計算 runner 已送出的 UTF-8 prompt 的約略 token 下限，需設定 `window_tokens`；它不知道 system prompt、工具輸出、歷史或 reasoning。`api_usage` 是 provider adapter 預留模式；一般 tmux interactive pane 尚無 usage feed 時會回報未知，threshold mode 不會因此自動 compact。
