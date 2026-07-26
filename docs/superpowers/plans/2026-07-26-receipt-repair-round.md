# Receipt Repair Round Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Recover file handoff turns whose current completion marker was emitted but whose current status receipt is missing or malformed.

**Architecture:** Add a bounded repair protocol that is separate from normal work rounds. The handoff engine detects a file-turn receipt failure after the original completion marker, asks the same pane for a nonce-bound receipt only, locally validates that receipt against the original status marker, then resumes existing artifact collection and coordinator routing.

**Tech Stack:** Go, Cobra, tmux executor fakes, existing handoff protocol and artifact store.

## Global Constraints

- Keep original per-run/per-round marker validation strict.
- Never infer outcome or artifact claims from terminal content or outbox presence.
- Limit a repair to one request per original round.
- Do not run the LLM screen selector for this file-transport failure mode.
- Preserve existing text-transport recovery behavior.

---

### Task 1: Define and test the repair control protocol

**Files:**
- Modify: `cmd/handoff_engine.go`
- Test: `cmd/handoff_protocol_test.go`

**Interfaces:**
- Produces: `receiptRepairProtocol` with original status marker, repair status marker, and repair completion marker.
- Consumes: `roundControlProtocol` and a cryptographically fresh repair token.

- [ ] **Step 1: Write failing protocol tests**

```go
func TestReceiptRepairRequiresFreshCompletionAndOriginalStatusMarker(t *testing.T) {
    original := newRoundControlProtocol(handoffTransportFile, "run", 7, "/tmp/outbox", true)
    repair := newReceiptRepairProtocol(original, "repair")
    output := original.StatusMarker + ` {"version":1,"outcome":"needs_followup","summary":"done"}` + "\n" + repair.DoneMarker
    if _, err := parseReceiptRepair(output, original.StatusMarker, repair.DoneMarker); err != nil {
        t.Fatal(err)
    }
}
```

- [ ] **Step 2: Run the focused test and verify it fails because the repair protocol does not exist**

Run: `go test ./cmd -run TestReceiptRepairRequiresFreshCompletionAndOriginalStatusMarker -count=1`

- [ ] **Step 3: Implement the minimal repair marker generator and parser**

```go
type receiptRepairProtocol struct { DoneMarker string }

func newReceiptRepairProtocol(original roundControlProtocol, token string) receiptRepairProtocol
func parseReceiptRepair(output, originalStatusMarker, repairDoneMarker string) (*handoffStatus, error)
```

- [ ] **Step 4: Run the focused test and verify it passes**

Run: `go test ./cmd -run TestReceiptRepairRequiresFreshCompletionAndOriginalStatusMarker -count=1`

### Task 2: Add a bounded same-pane receipt repair path

**Files:**
- Modify: `cmd/handoff_engine.go`
- Test: `cmd/handoff_protocol_test.go`

**Interfaces:**
- Consumes: `handoffEngine.Executor`, `handoffEngine.Guard`, the original `roundControlProtocol`, and the completed pane capture.
- Produces: the original round's validated `turnResult`, or a diagnostic error containing both receipt and repair failures.

- [ ] **Step 1: Write failing engine tests**

```go
func TestFileTurnRepairsMissingStatusFromSamePane(t *testing.T) {
    // Fake executor first returns original DONE without the original status,
    // then returns the original status plus the repair DONE marker.
    // Assert one repair prompt targets the same pane and artifacts are collected.
}

func TestFileTurnRejectsStaleOrMalformedRepairReceipt(t *testing.T) {
    // Assert a stale original status marker and malformed JSON both stop safely.
}
```

- [ ] **Step 2: Run the focused tests and verify they fail because no repair is attempted**

Run: `go test ./cmd -run 'TestFileTurn(RepairsMissingStatusFromSamePane|RejectsStaleOrMalformedRepairReceipt)' -count=1`

- [ ] **Step 3: Implement one repair request only for file transport receipt failures after current completion**

```go
func (e handoffEngine) repairMissingFileReceipt(
    ctx context.Context, current handoffMember, original roundControlProtocol,
    incoming []artifact.Ref, store artifact.Store, originalErr error,
) (turnResult, error)
```

The method must call `sendToPane` for `current.Target`, wait only for the fresh repair marker, parse the receipt using `original.StatusMarker`, and pass the recovered status through existing `prepareTurnResultFromOutput` artifact validation.

- [ ] **Step 4: Run the focused tests and verify they pass**

Run: `go test ./cmd -run 'TestFileTurn(RepairsMissingStatusFromSamePane|RejectsStaleOrMalformedRepairReceipt)' -count=1`

### Task 3: Verify regression coverage

**Files:**
- Test: `cmd/handoff_protocol_test.go`

- [ ] **Step 1: Add a test proving text transport does not invoke receipt repair**

```go
func TestTextTurnDoesNotUseFileReceiptRepair(t *testing.T) {
    // A validated text frame without status retains existing needs_followup recovery.
}
```

- [ ] **Step 2: Run focused and full tests**

Run: `go test ./cmd -count=1 && go test ./...`

- [ ] **Step 3: Review the diff for scope and formatting**

Run: `gofmt -w cmd/handoff_engine.go cmd/handoff_protocol_test.go && git diff --check`
