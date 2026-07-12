package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/llmore/tmux-ai/internal/timeparser"
	"github.com/spf13/cobra"
)

var (
	schedulePaneFlag    string
	scheduleSessionFlag string
	scheduleWindowFlag  string
)

var scheduleCmd = &cobra.Command{
	Use:   "schedule <time-expr> <instruction>",
	Short: "在指定時間執行指令",
	Long: `解析自然語言時間，等到該時刻再讓 agent 執行指令。

時間格式範例：
  中文相對時間：10分鐘後、2小時後、30秒後、半小時後
  中文絕對時間：今晚七點、下午三點、明天早上九點、明天晚上八點半
  英文相對時間：in 10 minutes、in 2 hours
  英文絕對時間：at 9pm、tonight at 7、tomorrow at 9am

範例：
  tmux-ai schedule "10分鐘後" "執行 build"
  tmux-ai schedule "今晚七點" "提醒進行 code review" --pane claude-sport
  tmux-ai schedule "明天早上九點" "開始跑測試" --session github --window 3`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		timeExpr := args[0]
		instruction := args[1]
		return runSchedule(timeExpr, instruction, schedulePaneFlag, scheduleSessionFlag, scheduleWindowFlag)
	},
}

func init() {
	rootCmd.AddCommand(scheduleCmd)
	scheduleCmd.Flags().StringVarP(&schedulePaneFlag, "pane", "p", "", "目標 pane（%ID、alias 或 session:window.pane）")
	scheduleCmd.Flags().StringVarP(&scheduleSessionFlag, "session", "s", "", "目標 session")
	scheduleCmd.Flags().StringVarP(&scheduleWindowFlag, "window", "w", "", "目標 window（搭配 --session）")
}

func runSchedule(timeExpr, instruction, pane, session, window string) error {
	target, err := timeparser.Parse(timeExpr)
	if err != nil {
		return fmt.Errorf("時間解析失敗：%w", err)
	}

	now := time.Now()
	if target.Before(now) {
		return fmt.Errorf("時間已過：%s", target.Format("2006-01-02 15:04:05"))
	}

	wait := time.Until(target)
	fmt.Printf("\033[1;35m[schedule]\033[0m 將在 %s 執行\n", timeparser.Format(target))
	fmt.Printf("           指令：%s\n\n", instruction)

	timer := time.NewTimer(wait)
	defer timer.Stop()
	<-timer.C

	fmt.Printf("\033[1;35m[schedule]\033[0m 時間到，開始執行...\n\n")

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	resolver := newResolver()
	paneID := resolver.ResolvePaneFromFlags(pane, session, window)

	runner := newRunner()
	runner.TargetPane = paneID

	return runner.Run(ctx, instruction)
}
