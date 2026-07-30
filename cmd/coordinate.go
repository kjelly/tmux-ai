package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/llmore/tmux-ai/internal/tmux"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// coordinate extends pingpong's guarded handoff loop to an arbitrary roster
// of role-defined tmux windows/panes. The coordinator can route consecutive
// turns to the same member when that is the most appropriate next action.

var (
	coordPreset  string
	coordInitial string
)

type coordinatePreset struct {
	handoffPresetConfig `yaml:",inline"`
	Initial             string             `yaml:"initial"`
	CoordinatorPolicy   string             `yaml:"coordinator_policy"`
	StopMembers         []string           `yaml:"stop_members"`
	MaxSelectionRetries int                `yaml:"max_selection_retries"`
	CompletionSettleMS  *int               `yaml:"completion_settle_ms"`
	NewSession          bool               `yaml:"new_session"`
	Members             []coordinateMember `yaml:"members"`
}

type coordinateMember struct {
	Name          string `yaml:"name"`
	Target        string `yaml:"target"`
	Role          string `yaml:"role"`
	HandoffPrompt string `yaml:"handoff_prompt"`
	GoalMode      bool   `yaml:"goal_mode"`
}

var coordinateCmd = &cobra.Command{
	Use:   "coordinate",
	Short: "Route work among multiple role-defined tmux windows",
	Long: `Coordinate two or more AI agents running in tmux windows or panes.

The roster is defined by a YAML preset. Every member has a stable name,
tmux target, role, and optional handoff prompt. An LLM coordinator chooses the
best member for the initial task and after every completed turn.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ppHandoffFlagSet = cmd.Flags()
		return runCoordinate()
	},
}

func init() {
	rootCmd.AddCommand(coordinateCmd)
	coordinateCmd.Flags().StringVar(&coordPreset, "preset", "", "YAML file defining role-based members (required)")
	coordinateCmd.Flags().StringVar(&coordInitial, "initial", "", "Initial goal (overrides preset initial)")
	coordinateCmd.Flags().StringVar(&ppStop, "stop", "ALL_DONE", "Stop marker validated by coordinator policy")
	coordinateCmd.Flags().StringVar(&ppDone, "done", "DONE", "Marker an agent prints after one turn")
	coordinateCmd.Flags().IntVar(&ppMaxRounds, "max-rounds", 20, "Maximum coordinated rounds")
	coordinateCmd.Flags().IntVar(&ppInterval, "interval", 2000, "Poll interval in milliseconds")
	coordinateCmd.Flags().IntVar(&ppLines, "lines", 256, "Pane lines captured per turn")
	coordinateCmd.Flags().IntVar(&ppRoundTimeout, "round-timeout", 600, "Per-round timeout in seconds (0 = no timeout)")
	coordinateCmd.Flags().IntVar(&ppCompletionSettleTimeout, "completion-settle-timeout", 1000, "Milliseconds a validated completion envelope must remain unchanged before handoff (0 disables)")
	coordinateCmd.Flags().IntVar(&ppCoordTimeout, "coordinator-timeout", 60, "Coordinator timeout in seconds")
	coordinateCmd.Flags().StringVar(&ppCoordinatorPolicy, "coordinator-policy", "", "Trusted workflow policy used for routing and stop decisions")
	coordinateCmd.Flags().StringSliceVar(&ppStopMembers, "stop-members", nil, "Member IDs allowed to approve coordinator stop")
	coordinateCmd.Flags().IntVar(&ppMaxSelectionRetries, "max-selection-retries", 2, "Maximum response reselections per round")
	coordinateCmd.Flags().IntVar(&ppSubmitDelay, "submit-delay", 500, "Delay in milliseconds before pressing Enter")
	coordinateCmd.Flags().IntVar(&ppSubmitConfirmTimeout, "submit-confirm-timeout", 5000, "Milliseconds to confirm prompt submission")
	coordinateCmd.Flags().IntVar(&ppSubmitRetries, "submit-retries", 1, "Additional Enter attempts")
	coordinateCmd.Flags().IntVar(&ppMaxRelayBytes, "max-relay-bytes", 32*1024, "Maximum bytes in one outbound prompt")
	coordinateCmd.Flags().IntVar(&ppMaxTotalRelayBytes, "max-total-relay-bytes", 128*1024, "Maximum total outbound bytes")
	coordinateCmd.Flags().IntVar(&ppMaxLLMCalls, "max-llm-calls", 40, "Maximum coordinator/selector calls")
	coordinateCmd.Flags().IntVar(&ppMaxSamePaneRounds, "max-same-window-rounds", 3, "Maximum consecutive sends to one member")
	coordinateCmd.Flags().IntVar(&ppMaxDuplicateHandoffs, "max-duplicate-handoffs", 2, "Maximum identical handoffs")
	coordinateCmd.Flags().IntVar(&ppMinResponseLines, "min-response-lines", 1, "Minimum response lines for a handoff")
	coordinateCmd.Flags().IntVar(&ppMaxResponseLines, "max-response-lines", 200, "Maximum response lines for a handoff")
	coordinateCmd.Flags().Float64Var(&ppMinCoordConfidence, "min-coordinator-confidence", 0.7, "Minimum confidence for a send")
	coordinateCmd.Flags().IntVar(&ppApprovalThresholdBytes, "approval-threshold-bytes", 16*1024, "Prompt size requiring --allow-large-relay")
	coordinateCmd.Flags().BoolVar(&ppAllowLargeRelay, "allow-large-relay", false, "Allow prompts above the approval threshold")
	coordinateCmd.Flags().BoolVar(&ppDryRun, "dry-run", false, "Print the first handoff without sending it")
	coordinateCmd.Flags().BoolVar(&ppNewSession, "new", false, "Send /new to every participating pane before starting the workflow")
	addHandoffFlags(coordinateCmd)
	_ = coordinateCmd.MarkFlagRequired("preset")
}

func loadCoordinatePreset(path string) (coordinatePreset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return coordinatePreset{}, fmt.Errorf("read coordinate preset %q: %w", path, err)
	}
	var preset coordinatePreset
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&preset); err != nil {
		return coordinatePreset{}, appendYAMLCommandHint(fmt.Errorf("parse coordinate preset %q: %w", path, err), data, "coordinate preset")
	}
	if err := validateCoordinateMembers(preset.Members); err != nil {
		return coordinatePreset{}, appendYAMLCommandHint(err, data, "coordinate preset")
	}
	return preset, nil
}

func validateCoordinateMembers(members []coordinateMember) error {
	if len(members) < 2 {
		return fmt.Errorf("coordinate preset requires at least two members")
	}
	names := make(map[string]bool, len(members))
	targets := make(map[string]bool, len(members))
	for i := range members {
		m := &members[i]
		m.Name = strings.TrimSpace(m.Name)
		m.Target = strings.TrimSpace(m.Target)
		m.Role = strings.TrimSpace(m.Role)
		m.HandoffPrompt = strings.TrimSpace(m.HandoffPrompt)
		if m.Name == "" || m.Target == "" || m.Role == "" {
			return fmt.Errorf("member %d must define name, target, and role", i+1)
		}
		key := strings.ToLower(m.Name)
		if names[key] {
			return fmt.Errorf("duplicate member name %q", m.Name)
		}
		if targets[m.Target] {
			return fmt.Errorf("duplicate member target %q", m.Target)
		}
		names[key] = true
		targets[m.Target] = true
	}
	return nil
}

func runCoordinate() error {
	preset, err := loadCoordinatePreset(coordPreset)
	if err != nil {
		return err
	}
	transport, err := resolveHandoffConfig(ppHandoffFlagSet, preset.handoffPresetConfig)
	if err != nil {
		return err
	}
	initial := strings.TrimSpace(coordInitial)
	policy := strings.TrimSpace(ppCoordinatorPolicy)
	if policy == "" {
		policy = strings.TrimSpace(preset.CoordinatorPolicy)
	}
	stopMemberValues := ppStopMembers
	if len(stopMemberValues) == 0 {
		stopMemberValues = preset.StopMembers
	}
	if preset.MaxSelectionRetries > 0 {
		ppMaxSelectionRetries = preset.MaxSelectionRetries
	}
	if preset.CompletionSettleMS != nil {
		ppCompletionSettleTimeout = *preset.CompletionSettleMS
	}
	if ppHandoffFlagSet == nil || !ppHandoffFlagSet.Changed("new") {
		ppNewSession = preset.NewSession
	}
	if initial == "" {
		initial = strings.TrimSpace(preset.Initial)
	}
	if initial == "" {
		return fmt.Errorf("initial goal is required via --initial or preset initial")
	}
	if ppMaxRounds <= 0 || ppLines <= 0 || ppMaxRelayBytes <= 0 || ppMaxTotalRelayBytes <= 0 || ppMaxLLMCalls <= 0 {
		return fmt.Errorf("round, line, relay, and LLM limits must be greater than zero")
	}
	if ppCompletionSettleTimeout < 0 {
		return fmt.Errorf("--completion-settle-timeout must not be negative")
	}
	if ppMaxSamePaneRounds <= 0 || ppMaxDuplicateHandoffs <= 0 || ppMinResponseLines <= 0 || ppMaxResponseLines < ppMinResponseLines {
		return fmt.Errorf("handoff and response limits are invalid")
	}
	if ppMaxSelectionRetries < 0 {
		return fmt.Errorf("--max-selection-retries must not be negative")
	}
	if ppMinCoordConfidence < 0 || ppMinCoordConfidence > 1 {
		return fmt.Errorf("--min-coordinator-confidence must be between 0 and 1")
	}

	resolver := newResolver()
	members := append([]coordinateMember(nil), preset.Members...)
	byName := make(map[string]coordinateMember, len(members))
	for i := range members {
		// Preserve logical session:window targets across pane replacement.
		members[i].Target = resolver.Aliases.Resolve(members[i].Target)
		key := strings.ToLower(members[i].Name)
		byName[key] = members[i]
	}
	guard := newHandoffGuard()
	ex := tmux.NewExecutor()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	coordinatorMembers := coordinateCoordinatorMembers(members)
	stopMembers, err := normalizeCoordinatorStopMembers(stopMemberValues, coordinatorMembers)
	if err != nil {
		return fmt.Errorf("invalid stop_members: %w", err)
	}
	decision, err := decideHandoffAction(ctx, handoffCoordinatorRequest{
		Initial: initial, Policy: policy, InitialRoute: true, Members: coordinatorMembers, StopMembers: stopMembers, Transport: transport.Transport,
		TotalBytes: guard.totalBytes, LLMCalls: guard.llmCalls,
	}, guard)
	if err != nil {
		return fmt.Errorf("select initial member: %w", err)
	}
	current, err := approvedCoordinateMember(decision, byName)
	if err != nil {
		return err
	}

	engineMembers := make(map[string]handoffMember, len(members))
	resetMembers := make([]handoffMember, 0, len(members))
	for _, member := range members {
		id := strings.ToLower(member.Name)
		engineMember := handoffMember{ID: id, Label: member.Name, Target: member.Target, Role: member.Role, GoalMode: member.GoalMode}
		engineMembers[id] = engineMember
		resetMembers = append(resetMembers, engineMember)
	}
	router := func(ctx context.Context, round int, current handoffMember, result turnResult, stopRequested bool) (handoffRoute, error) {
		payloadInfo := textPayloadMetadata(result.Payload.Text)
		next, err := decideHandoffAction(ctx, handoffCoordinatorRequest{
			Initial: initial, Policy: policy, Round: round,
			CurrentID: current.ID, CurrentOutput: result.Narrative, StopRequested: stopRequested, StopMembers: stopMembers,
			Transport: transport.Transport, CurrentStatus: result.Status, TextPayloadBytes: payloadInfo.Bytes, TextPayloadLines: payloadInfo.Lines, TextPayloadSHA256: payloadInfo.SHA256,
			IncomingArtifacts: result.Incoming, ProducedArtifacts: result.Produced, AvailableArtifacts: result.AvailableArtifacts(),
			Members: coordinatorMembers, PayloadBytes: len([]byte(result.Narrative)),
			PayloadLines: responseLineCount(result.Narrative), TotalBytes: guard.totalBytes, LLMCalls: guard.llmCalls,
		}, guard)
		if err != nil {
			return handoffRoute{}, fmt.Errorf("coordinator gate failed: %w", err)
		}
		return routeFromCoordinatorDecision(next)
	}

	fmt.Printf("🧠 Coordinator selected initial member %s: %s\n", current.Name, truncate(decision.Reason, 100))
	return handoffEngine{
		Executor: ex, Guard: guard, Members: engineMembers, StartID: strings.ToLower(current.Name),
		InitialInstruction: decision.Instruction, InitialFingerprint: initial,
		ResetBeforeStart: resetMembers,
		SelectResponses:  true, CoordinatorControlsStop: true, Transport: transport, Route: router,
	}.Run(ctx)
}

func approvedCoordinateMember(decision handoffCoordinatorDecision, members map[string]coordinateMember) (coordinateMember, error) {
	if decision.Action != "send" {
		return coordinateMember{}, fmt.Errorf("coordinator gate requested %s: %s", decision.Action, decision.Reason)
	}
	if decision.Confidence < ppMinCoordConfidence {
		return coordinateMember{}, fmt.Errorf("coordinator confidence %.2f is below minimum %.2f", decision.Confidence, ppMinCoordConfidence)
	}
	member, ok := members[strings.ToLower(decision.Target)]
	if !ok {
		return coordinateMember{}, fmt.Errorf("coordinator selected unknown member %q", decision.Target)
	}
	return member, nil
}

func coordinateCoordinatorMembers(members []coordinateMember) []handoffCoordinatorMember {
	result := make([]handoffCoordinatorMember, 0, len(members))
	for _, member := range members {
		result = append(result, handoffCoordinatorMember{
			ID: strings.ToLower(member.Name), Label: member.Name, Role: member.Role,
			DefaultInstruction: member.HandoffPrompt,
		})
	}
	return result
}

func parseCoordinateDecision(output string, members []coordinateMember) (handoffCoordinatorDecision, error) {
	return parseHandoffCoordinatorDecision(output, coordinateCoordinatorMembers(members))
}
