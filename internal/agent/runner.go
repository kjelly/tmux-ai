package agent

import (
	"context"
	cfg "github.com/llmore/tmux-ai/internal/config"
	"fmt"
	"os/exec"

	"github.com/llmore/tmux-ai/internal/ollama"
	"github.com/llmore/tmux-ai/internal/tmux"
)


type Runner struct {
	ollamaClient      ollama.Client
	tmux              tmux.Executor
	tools             *ToolRegistry
	maxIterations     int
	verbose           bool
	CheckpointFile    string
	ReportFile        string
	TargetPane        string // hint: agent should focus on this pane
	ExtraContext      string // prepended as "Captured context" in the initial message
	LastOutput        string // final text response from the agent (set after Run completes)
	SkipGlobalContext bool   // set true to suppress ~/.config/tmux-ai/context.md
}

func NewRunner(ollamaHost, model string, maxIterations int, verbose bool) *Runner {
	r := &Runner{
		ollamaClient:  ollama.NewClient(ollamaHost, model),
		tmux:          tmux.NewExecutor(),
		maxIterations: maxIterations,
		verbose:       verbose,
	}
	r.tools = NewToolRegistry()
	registerAllTools(r.tools, r.tmux, verbose)
	// send_notification needs access to the Runner for terminal notifications
	r.registerNotificationTool()
	return r
}

func (r *Runner) Run(ctx context.Context, instruction string) error {
	var messages []ollama.Message
	defer func() {
		if r.ReportFile != "" && len(messages) > 0 {
			r.generateReport(messages)
		}
	}()

	if saved, ok := r.loadCheckpoint(); ok {
		messages = saved
	}
	if len(messages) == 0 {
		tmuxState, err := r.getTmuxState()
		if err != nil {
			return fmt.Errorf("get tmux state: %w", err)
		}

		extra := r.ExtraContext
		if !r.SkipGlobalContext {
			if gc := cfg.LoadGlobalContext(); gc != "" {
				if extra != "" {
					extra = "Global context:\n" + gc + "\n\n" + extra
				} else {
					extra = "Global context:\n" + gc
				}
			}
		}

		messages = []ollama.Message{
			{Role: "system", Content: buildSystemPrompt(r.tools)},
			BuildInitialMessage(instruction, tmuxState, r.TargetPane, extra),
		}
	}

	for i := 0; i < r.maxIterations; i++ {
		if r.verbose {
			fmt.Printf("[Iteration %d] Calling Ollama...\n", i+1)
		}

		resp, err := r.ollamaClient.Chat(ctx, messages, r.tools.Definitions())
		if err != nil {
			return fmt.Errorf("ollama chat: %w", err)
		}

		if len(resp.Message.ToolCalls) > 0 {
			if r.verbose {
				fmt.Printf("[Iteration %d] Got %d tool calls\n", i+1, len(resp.Message.ToolCalls))
			}

			messages = append(messages, resp.Message)

			for _, tc := range resp.Message.ToolCalls {
				result := r.tools.Execute(ctx, tc)
				messages = append(messages, ollama.Message{
					Role:      "tool",
					ToolCallID: tc.ID,
					Content:   result,
				})
			}
			r.saveCheckpoint(messages)
			continue
		}

		if resp.Message.Content != "" {
			fmt.Println(resp.Message.Content)
			r.LastOutput = resp.Message.Content
			r.saveCheckpoint(messages)
			return nil
		}
	}

	return fmt.Errorf("max iterations (%d) reached", r.maxIterations)
}


// registerNotificationTool registers the send_notification tool which needs
// access to the Runner's sendTerminalNotification method.
func (r *Runner) registerNotificationTool() {
	r.tools.Register("send_notification", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "send_notification",
			Description: "Send a system desktop notification or terminal alert to notify the user of an important event, progress milestone, or required action.",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"title": {"type": "string", "description": "The title of the notification"},
					"message": {"type": "string", "description": "The content/body of the notification"},
					"urgency": {"type": "string", "description": "Optional urgency level: low, normal, or critical"}
				},
				"required": ["title", "message"]
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		title := strArg(args, "title")
		message := strArg(args, "message")
		urgency := strArg(args, "urgency")
		if urgency == "" {
			urgency = "normal"
		}
		cmd := exec.Command("notify-send", "-u", urgency, title, message)
		err := cmd.Run()
		urgencyColor := "\033[36m"
		if urgency == "critical" {
			urgencyColor = "\033[31m"
		} else if urgency == "low" {
			urgencyColor = "\033[37m"
		}
		resetColor := "\033[0m"

		r.sendTerminalNotification(title, message)

		fmt.Printf("\a\n%s[NOTIFICATION] %s: %s%s\n", urgencyColor, title, message, resetColor)
		if err != nil {
			return "notification printed to console (system notification failed or unavailable)"
		}
		return "notification sent successfully"
	})
}
