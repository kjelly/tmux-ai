# Receipt Repair Round Design

## Goal

Recover a file-handoff turn when an agent emitted the current round's completion marker but omitted or corrupted its current round status receipt, without trusting stale terminal text or inferring an outcome from artifacts.

## Trust Boundary

The runner continues to require the original round's unique `DONE` or `ALL_DONE` marker. It never accepts a status receipt from a prior run or round. The original outbox remains the only artifact source; all artifacts continue through the existing bounded snapshot and hash verification.

## Repair Transaction

When file transport detects a current completion marker but cannot parse the current status receipt, it starts one bounded repair transaction for that round. The runner sends the same pane a minimal instruction containing:

- the original round token and expected status marker;
- a fresh random repair token and repair completion marker;
- the original outbox path;
- a requirement to output only a machine-readable receipt followed by the repair completion marker;
- a prohibition on creating or modifying artifacts.

The receipt has the existing status schema and identifies the original status marker. The repair response is accepted only when it contains the fresh repair completion marker and exactly one valid receipt for that original marker. The runner then collects and verifies the original outbox artifacts using the recovered claims and continues normal coordinator routing.

## Failure Handling

The repair transaction is attempted at most once per original round. A missing, malformed, stale, duplicate, or mismatched repair receipt stops the workflow with a diagnostic that preserves both the original protocol error and the repair error. A repaired receipt cannot independently stop a workflow: normal coordinator policy still decides whether an original `ALL_DONE` outcome is valid.

## Non-Goals

- Do not loosen per-round token matching.
- Do not invoke the full-screen LLM selector for a missing receipt in file transport.
- Do not synthesize an outcome from artifact presence.
- Do not support repair for text transport, which already has a validated text-frame recovery path.

## Tests

Cover successful repair, stale-token rejection, malformed-receipt rejection, repair completion marker absence, one-attempt limit, and that file artifact collection occurs only after a valid original or repaired receipt.
