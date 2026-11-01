package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/llmore/tmux-ai/internal/artifact"
)

const handoffJournalVersion = 1

type handoffJournalPhase string

const (
	journalAwaitingMember       handoffJournalPhase = "awaiting_member"
	journalSourceCommitted      handoffJournalPhase = "source_committed"
	journalRelayPrepared        handoffJournalPhase = "relay_prepared"
	journalRelaySubmitConfirmed handoffJournalPhase = "relay_submit_confirmed"
	journalRelaySubmitStarted   handoffJournalPhase = "relay_submit_started"
	journalCompleted            handoffJournalPhase = "completed"
)

// handoffRunState deliberately contains only data needed to restart at a
// verified handoff boundary. relay_submit_started is an explicit ambiguous
// state: resume must never replay it without an operator decision.
type handoffRunState struct {
	Version                int                  `json:"version"`
	UpdatedAt              time.Time            `json:"updated_at"`
	Phase                  handoffJournalPhase  `json:"phase"`
	RunToken               string               `json:"run_token"`
	ConfigSHA256           string               `json:"config_sha256"`
	Members                []journalMember      `json:"members"`
	ArtifactRoot           string               `json:"artifact_root,omitempty"`
	ArtifactManifestSHA256 string               `json:"artifact_manifest_sha256,omitempty"`
	Round                  int                  `json:"round"`
	CurrentID              string               `json:"current_id"`
	NextID                 string               `json:"next_id,omitempty"`
	Protocol               roundControlProtocol `json:"protocol"`
	IncomingIDs            []string             `json:"incoming_ids,omitempty"`
	Source                 *journalSource       `json:"source,omitempty"`
	Message                string               `json:"message,omitempty"`
	MessageSHA256          string               `json:"message_sha256,omitempty"`
	Guard                  journalGuard         `json:"guard"`
	Compaction             journalCompaction    `json:"compaction"`
}

type journalMember struct {
	ID, Target, Role string
	GoalMode         bool
}

type journalSource struct {
	MemberID     string         `json:"member_id"`
	Stopped      bool           `json:"stopped"`
	Narrative    string         `json:"narrative"`
	Status       *handoffStatus `json:"status,omitempty"`
	StatusSHA256 string         `json:"status_sha256"`
	PayloadText  string         `json:"payload_text,omitempty"`
	IncomingIDs  []string       `json:"incoming_ids,omitempty"`
	ProducedIDs  []string       `json:"produced_ids,omitempty"`
}

type journalGuard struct {
	TotalBytes, LLMCalls, SameTargetCount int
	LastTarget                            string
	HandoffCounts                         map[string]int
}

type journalCompaction struct {
	LastRound map[string]int `json:"last_round,omitempty"`
	Estimated map[string]int `json:"estimated_tokens,omitempty"`
}

func artifactRoot(store artifact.Store) string {
	if store == nil {
		return ""
	}
	return store.Root()
}

func digestString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func artifactIDs(refs []artifact.Ref) []string {
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.ID)
	}
	return ids
}

func artifactManifestDigest(store artifact.Store) string {
	if store == nil {
		return ""
	}
	data, err := json.Marshal(store.Manifest())
	if err != nil {
		return ""
	}
	return digestString(string(data))
}

func journalMembers(members map[string]handoffMember) []journalMember {
	ids := make([]string, 0, len(members))
	for id := range members {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]journalMember, 0, len(ids))
	for _, id := range ids {
		member := members[id]
		result = append(result, journalMember{ID: member.ID, Target: member.Target, Role: member.Role, GoalMode: member.GoalMode})
	}
	return result
}

func journalSourceFromResult(member handoffMember, stopped bool, result turnResult) *journalSource {
	return &journalSource{MemberID: member.ID, Stopped: stopped, Narrative: result.Narrative, Status: result.Status, StatusSHA256: digestStatus(result.Status), PayloadText: result.Payload.Text, IncomingIDs: artifactIDs(result.Incoming), ProducedIDs: artifactIDs(result.Produced)}
}

func (s journalSource) restore(store artifact.Store, transport handoffTransportKind) (turnResult, error) {
	if s.MemberID == "" || s.Status == nil || s.StatusSHA256 == "" || s.StatusSHA256 != digestStatus(s.Status) {
		return turnResult{}, errors.New("committed source state is incomplete")
	}
	var incoming, produced []artifact.Ref
	var err error
	if store != nil {
		if incoming, err = store.Resolve(s.IncomingIDs); err != nil {
			return turnResult{}, fmt.Errorf("resolve committed incoming artifacts: %w", err)
		}
		if produced, err = store.Resolve(s.ProducedIDs); err != nil {
			return turnResult{}, fmt.Errorf("resolve committed produced artifacts: %w", err)
		}
		if err := store.Verify(append(append([]artifact.Ref(nil), incoming...), produced...)); err != nil {
			return turnResult{}, fmt.Errorf("verify committed artifacts: %w", err)
		}
	} else if len(s.IncomingIDs) != 0 || len(s.ProducedIDs) != 0 {
		return turnResult{}, errors.New("committed artifacts require file transport")
	}
	return turnResult{Narrative: s.Narrative, Status: s.Status, Payload: handoffPayload{Transport: transport, Text: s.PayloadText}, Incoming: incoming, Produced: produced}, nil
}

func digestStatus(status *handoffStatus) string {
	if status == nil {
		return ""
	}
	data, err := json.Marshal(status)
	if err != nil {
		return ""
	}
	return digestString(string(data))
}

func (e handoffEngine) journalConfigDigest() (string, error) {
	compaction := struct {
		Enabled                     bool
		Members                     []string
		Mode                        string
		MinRoundsBetween, AtPercent int
		MeterMode                   string
		MeterPattern                string
		WindowTokens, BottomLines   int
		LLMMinConfidence            float64
	}{
		Enabled: e.Compaction.Enabled,
		Mode:    string(e.Compaction.Mode), MinRoundsBetween: e.Compaction.MinRoundsBetween, AtPercent: e.Compaction.AtPercent,
		MeterMode: string(e.Compaction.Meter.Mode), WindowTokens: e.Compaction.Meter.WindowTokens,
		BottomLines: e.Compaction.Meter.BottomLines, LLMMinConfidence: e.Compaction.Meter.LLMMinConfidence,
	}
	for member, enabled := range e.Compaction.Members {
		if enabled {
			compaction.Members = append(compaction.Members, member)
		}
	}
	sort.Strings(compaction.Members)
	if e.Compaction.Meter.Pattern != nil {
		compaction.MeterPattern = e.Compaction.Meter.Pattern.String()
	}
	data, err := json.Marshal(struct {
		StartID, InitialFingerprint string
		CoordinatorControlsStop     bool
		Transport                   resolvedHandoffConfig
		Compaction                  interface{}
		Members                     []journalMember
		MaxRounds                   int
	}{e.StartID, e.InitialFingerprint, e.CoordinatorControlsStop, e.Transport, compaction, journalMembers(e.Members), ppMaxRounds})
	if err != nil {
		return "", err
	}
	return digestString(string(data)), nil
}

func snapshotGuard(g *handoffGuard) journalGuard {
	if g == nil {
		return journalGuard{}
	}
	counts := make(map[string]int, len(g.handoffCounts))
	for hash, count := range g.handoffCounts {
		counts[hex.EncodeToString(hash[:])] = count
	}
	return journalGuard{TotalBytes: g.totalBytes, LLMCalls: g.llmCalls, LastTarget: g.lastTarget, SameTargetCount: g.sameTargetCount, HandoffCounts: counts}
}

func restoreGuard(g *handoffGuard, state journalGuard) error {
	if g == nil {
		return errors.New("handoff guard is required")
	}
	counts := make(map[[32]byte]int, len(state.HandoffCounts))
	for encoded, count := range state.HandoffCounts {
		decoded, err := hex.DecodeString(encoded)
		if err != nil || len(decoded) != sha256.Size || count < 1 {
			return errors.New("invalid handoff guard state")
		}
		var hash [32]byte
		copy(hash[:], decoded)
		counts[hash] = count
	}
	if state.TotalBytes < 0 || state.LLMCalls < 0 || state.SameTargetCount < 0 {
		return errors.New("invalid handoff guard counters")
	}
	g.totalBytes, g.llmCalls, g.lastTarget, g.sameTargetCount, g.handoffCounts = state.TotalBytes, state.LLMCalls, state.LastTarget, state.SameTargetCount, counts
	return nil
}

func snapshotCompactor(c *compactor) journalCompaction {
	if c == nil {
		return journalCompaction{}
	}
	lastRound := make(map[string]int, len(c.lastRound))
	for id, round := range c.lastRound {
		lastRound[id] = round
	}
	estimated := make(map[string]int, len(c.estimated))
	for id, tokens := range c.estimated {
		estimated[id] = tokens
	}
	return journalCompaction{LastRound: lastRound, Estimated: estimated}
}

func restoreCompactor(c *compactor, state journalCompaction) error {
	if c == nil {
		return errors.New("compactor is required")
	}
	for _, values := range []map[string]int{state.LastRound, state.Estimated} {
		for id, value := range values {
			if id == "" || value < 0 {
				return errors.New("invalid compaction state")
			}
		}
	}
	c.lastRound = mapsClone(state.LastRound)
	c.estimated = mapsClone(state.Estimated)
	return nil
}

func mapsClone(input map[string]int) map[string]int {
	result := make(map[string]int, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func (e handoffEngine) journalState(phase handoffJournalPhase, token string, store artifact.Store, compactor *compactor, round int, current, next handoffMember, protocol roundControlProtocol, incoming []artifact.Ref, source *journalSource, message string) (handoffRunState, error) {
	configDigest, err := e.journalConfigDigest()
	if err != nil {
		return handoffRunState{}, err
	}
	return handoffRunState{
		Phase: phase, RunToken: token, ConfigSHA256: configDigest, Members: journalMembers(e.Members),
		ArtifactRoot: artifactRoot(store), ArtifactManifestSHA256: artifactManifestDigest(store),
		Round: round, CurrentID: current.ID, NextID: next.ID, Protocol: protocol, IncomingIDs: artifactIDs(incoming), Source: source,
		Message: message, MessageSHA256: digestString(message), Guard: snapshotGuard(e.Guard), Compaction: snapshotCompactor(compactor),
	}, nil
}

type handoffJournal struct{ path string }

func newHandoffJournal(path string) (*handoffJournal, error) {
	if path == "" {
		return nil, nil
	}
	if !filepath.IsAbs(path) {
		return nil, errors.New("run-state path must be absolute")
	}
	return &handoffJournal{path: path}, nil
}

func (j *handoffJournal) save(state handoffRunState) error {
	if j == nil {
		return nil
	}
	state.Version = handoffJournalVersion
	state.UpdatedAt = time.Now().UTC()
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return atomicReplaceFile(j.path, data)
}

func loadHandoffRunState(path string) (handoffRunState, error) {
	j, err := newHandoffJournal(path)
	if err != nil || j == nil {
		return handoffRunState{}, err
	}
	data, err := os.ReadFile(j.path)
	if err != nil {
		return handoffRunState{}, err
	}
	var state handoffRunState
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return handoffRunState{}, fmt.Errorf("parse handoff run state: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return handoffRunState{}, errors.New("handoff run state must contain exactly one JSON object")
	}
	if state.Version != handoffJournalVersion || state.Round <= 0 || state.CurrentID == "" || state.RunToken == "" || state.ConfigSHA256 == "" || len(state.Members) == 0 {
		return handoffRunState{}, errors.New("handoff run state is incomplete")
	}
	if state.Message != "" && state.MessageSHA256 != digestString(state.Message) {
		return handoffRunState{}, errors.New("handoff run-state message digest does not match")
	}
	switch state.Phase {
	case journalAwaitingMember, journalSourceCommitted, journalRelayPrepared, journalRelaySubmitConfirmed, journalRelaySubmitStarted, journalCompleted:
	default:
		return handoffRunState{}, fmt.Errorf("unknown handoff run-state phase %q", state.Phase)
	}
	return state, nil
}
