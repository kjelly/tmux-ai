package agent

import (
	"fmt"
	"html/template"
	"os"
	"strings"
	"time"

	"github.com/llmore/tmux-ai/internal/ollama"
)

// generateReport writes a formatted execution trace report to r.ReportFile.
// The output format (Markdown or HTML) is determined by the file extension.
func (r *Runner) generateReport(messages []ollama.Message) {
	if r.ReportFile == "" {
		return
	}

	isMarkdown := strings.HasSuffix(r.ReportFile, ".md") || strings.HasSuffix(r.ReportFile, ".markdown")

	type TemplateData struct {
		Time     string
		Steps    int
		Success  bool
		Messages []ollama.Message
	}

	data := TemplateData{
		Time:     time.Now().Format("2006-01-02 15:04:05"),
		Steps:    len(messages),
		Success:  r.LastOutput != "",
		Messages: messages,
	}

	funcs := template.FuncMap{
		"add": func(a, b int) int {
			return a + b
		},
		"jsonToString": func(b []byte) string {
			return string(b)
		},
	}

	var tmplSrc string
	if isMarkdown {
		tmplSrc = `# tmux-ai 執行軌跡報告

* **產出時間:** {{.Time}}
* **決策步數:** {{.Steps}}
* **執行狀態:** {{if .Success}}✓ 執行成功{{else}}✗ 執行中斷 / 失敗{{end}}

---

## 決策與工具呼叫時間軸

{{range $i, $m := .Messages}}
### 步驟 #{{add $i 1}} - **{{$m.Role}}**

{{if $m.Content}}
#### 思考與內容
BKTBKTBKTtext
{{$m.Content}}
BKTBKTBKT
{{end}}

{{if $m.ToolCalls}}
#### 呼叫工具
{{range $tc := $m.ToolCalls}}
* **工具名稱:** BKT{{$tc.Function.Name}}BKT
* **參數:**
  BKTBKTBKTjson
  {{jsonToString $tc.Function.Arguments}}
  BKTBKTBKT
{{end}}
{{end}}

---
{{end}}`
		tmplSrc = strings.ReplaceAll(tmplSrc, "BKT", "`")
	} else {
		tmplSrc = `<!DOCTYPE html>
<html lang="zh-TW" class="dark">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>tmux-ai 執行軌跡報告</title>
    <script src="https://cdn.tailwindcss.com"></script>
    <script>
        tailwind.config = {
            darkMode: 'class',
            theme: {
                extend: {
                    colors: {
                        brand: {
                            50: '#f0f9ff',
                            100: '#e0f2fe',
                            500: '#0ea5e9',
                            600: '#0284c7',
                            900: '#0c4a6e',
                        }
                    }
                }
            }
        }
    </script>
    <style>
        body {
            background-color: #0f172a;
            color: #e2e8f0;
            font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
        }
    </style>
</head>
<body class="p-6 md:p-12 min-h-screen">
    <div class="max-w-5xl mx-auto space-y-8">
        <!-- Header -->
        <div class="flex flex-col md:flex-row md:items-center justify-between bg-slate-800/50 p-6 rounded-2xl border border-slate-700/50 backdrop-blur-sm gap-4">
            <div>
                <h1 class="text-3xl font-extrabold text-white tracking-tight flex items-center gap-2">
                    <span class="text-sky-400">tmux-ai</span> 執行軌跡報告
                </h1>
                <p class="text-sm text-slate-400 mt-1">產出時間: {{.Time}} | 步數: {{.Steps}}</p>
            </div>
            <div>
                {{if .Success}}
                <span class="inline-flex items-center px-4 py-2 rounded-full text-sm font-semibold bg-emerald-500/10 text-emerald-400 border border-emerald-500/20">
                    ✓ 執行成功
                </span>
                {{else}}
                <span class="inline-flex items-center px-4 py-2 rounded-full text-sm font-semibold bg-rose-500/10 text-rose-400 border border-rose-500/20">
                    ✗ 執行中斷 / 失敗
                </span>
                {{end}}
            </div>
        </div>

        <!-- Timeline -->
        <div class="space-y-6">
            <h2 class="text-xl font-bold text-white flex items-center gap-2">
                <span class="w-1.5 h-6 bg-sky-500 rounded-full"></span>
                決策與工具呼叫時間軸
            </h2>
            
            <div class="relative border-l border-slate-700 ml-4 space-y-8">
                {{range $i, $m := .Messages}}
                <div class="relative pl-8 group">
                    <!-- Point -->
                    <div class="absolute -left-[25px] top-1.5 w-4 h-4 rounded-full border-2 border-slate-900 bg-slate-700 group-hover:bg-sky-500 transition-colors duration-200"></div>
                    
                    <!-- Content Card -->
                    <div class="bg-slate-800/40 border border-slate-700/50 rounded-xl p-5 hover:border-slate-600 transition-colors duration-200 space-y-3">
                        <div class="flex items-center justify-between">
                            <span class="text-xs font-mono uppercase tracking-wider px-2.5 py-1 rounded bg-slate-900/60 {{if eq $m.Role "system"}}text-slate-400{{else if eq $m.Role "user"}}text-sky-400{{else if eq $m.Role "assistant"}}text-indigo-400{{else}}text-amber-400{{end}}">
                                {{$m.Role}}
                            </span>
                            <span class="text-xs text-slate-500">步驟 #{{add $i 1}}</span>
                        </div>
                        
                        <!-- Content Body -->
                        <div class="text-sm text-slate-300 leading-relaxed whitespace-pre-wrap font-mono">{{$m.Content}}</div>

                        <!-- Tool Calls -->
                        {{if $m.ToolCalls}}
                        <div class="mt-4 pt-3 border-t border-slate-700/50 space-y-2">
                            <span class="text-xs font-bold text-slate-400 uppercase tracking-wide">呼叫工具:</span>
                            {{range $tc := $m.ToolCalls}}
                            <div class="bg-slate-900/60 p-3 rounded-lg border border-slate-800 font-mono text-xs text-indigo-300 space-y-1">
                                <div class="font-bold text-indigo-400">{{$tc.Function.Name}}</div>
                                <div class="text-slate-400 whitespace-pre-wrap">{{jsonToString $tc.Function.Arguments}}</div>
                            </div>
                            {{end}}
                        </div>
                        {{end}}
                    </div>
                </div>
                {{end}}
            </div>
        </div>
    </div>
</body>
</html>`
	}

	tmpl, err := template.New("report").Funcs(funcs).Parse(tmplSrc)
	if err != nil {
		if r.verbose {
			fmt.Printf("Error parsing template: %v\n", err)
		}
		return
	}

	file, err := os.Create(r.ReportFile)
	if err != nil {
		if r.verbose {
			fmt.Printf("Error creating report file: %v\n", err)
		}
		return
	}
	defer file.Close()

	if err := tmpl.Execute(file, data); err != nil {
		if r.verbose {
			fmt.Printf("Error executing template: %v\n", err)
		}
	} else if r.verbose {
		fmt.Printf("Successfully generated report at: %s\n", r.ReportFile)
	}
}
