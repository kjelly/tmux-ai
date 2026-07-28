# Receipt Repair Round Design

## Goal

Recover a file-handoff turn when an agent emitted the current round's completion marker but omitted or corrupted its current round status receipt, without trusting stale terminal text or inferring an outcome from artifacts.

## Trust Boundary

The runner continues to require the original round's unique `DONE` or `ALL_DONE` marker. It never accepts a status receipt from a prior run or round. Prompt text must never itself form a parseable control record: marker values are described after semantic labels and JSON examples are shown separately. The original outbox is the only artifact source; the runner enumerates, snapshots, and hashes its safe files without accepting an agent-supplied artifact path list.

## Repair Transaction

When file transport detects a current completion marker but cannot parse the current status receipt, it starts a bounded repair transaction for that round. The runner sends the same pane a minimal instruction containing:

- the original round token and expected status marker;
- a fresh random repair token and repair completion marker;
- the original outbox path, which remains read-only during repair;
- a requirement to output only a machine-readable receipt followed by the repair completion marker, with `outcome` selected as one concrete value rather than an enum description;
- a prohibition on creating or modifying artifacts.

The receipt has the existing status schema except that file transport does not request artifact claims; it identifies the original status marker. The repair response is accepted only when it contains the fresh repair completion marker and exactly one valid receipt for that original marker. If local schema validation rejects that receipt, the runner sends at most one corrective retry with a new nonce and a field-specific safe correction. The runner then collects and verifies the original outbox artifacts independently and continues normal coordinator routing.

## Failure Handling

The repair transaction permits at most two receipt attempts per original round: an initial request and one corrective retry only after local receipt validation fails. A missing, malformed, stale, duplicate, or mismatched receipt after that bound stops the workflow with a diagnostic that preserves both the original protocol error and the repair error. A repaired receipt cannot independently stop a workflow: normal coordinator policy still decides whether an original `ALL_DONE` outcome is valid.

## Non-Goals

- Do not loosen per-round token matching.
- Do not invoke the full-screen LLM selector for a missing receipt in file transport.
- Do not synthesize an outcome from artifact presence.
- Do not support repair for text transport, which already has a validated text-frame recovery path.

## Tests

Cover successful repair, stale-token rejection, malformed-receipt rejection, repair completion marker absence, concrete enum examples, one corrective retry, prompt text that cannot be parsed as control output, and outbox discovery that ignores agent artifact claims.
