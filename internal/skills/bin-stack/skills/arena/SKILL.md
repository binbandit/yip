---
name: arena
description: Runs N competing attempts at the same task, cross-judges them, picks a base, and grafts the strongest ideas from the losers into it. Use when the user asks for arena, /arena, competing attempts, "arena this", "throw it in the arena", or when a single pass at a non-trivial artifact would lock in the wrong shape.
license: MIT
metadata:
  author: binbandit
  harness: cursor,claude,codex,pi
---

# Arena

Fan out N attempts at the same task. Read every candidate end to end. Pick the strongest as the base. Graft the best ideas from the others into it. Verify the result.

## Start

Open a todolist with one entry per phase before launching anything:

1. Frame
2. Fan out
3. Cross-judge
4. Pick
5. Graft
6. Verify

## Phase A: Frame

Every candidate gets the same prompt, so the prompt is the contract.

1. State the artifact each candidate is producing.
2. Derive the rubric. Say what success looks like for this task, then turn it into 3-6 concrete, gradeable criteria. The rubric is for the picker in Phase D. Candidates only see the task.
3. Pick the runners. If the harness can assign distinct models, prefer different model families for competing attempts. If only one model is available, still run N attempts on it. Prefer more seats when the arena covers multiple design directions. Prefer the same model N times when the work is generation-bound rather than judgment-sensitive.
4. Assign output paths. Each candidate writes to its own location (a git worktree when possible, otherwise a dedicated directory such as `/tmp/arena-<slug>/candidate-<n>/`). Do not let candidates mutate the same shared files or branch while they run.

## Phase B: Fan out

Give every candidate the same task, the path to any shared grounding (read-only), its own output path, and instructions to produce both the artifact and a short rationale.

Each rationale should name the alternatives the candidate considered and what it rejected.

If the harness can run parallel subagents, launch all N that way. Otherwise run the attempts sequentially in this session. Same prompt and isolation either way.

If a candidate fails to produce output, continue with N-1 and note the dropout in the synthesis record.

## Phase C: Cross-judge

After all Phase B candidates finish, run one readonly judge pass. If multiple models are available, pick a judge from a different model family than the authoring parent when possible. The judge sees the rubric and the candidates by path label, scores each criterion, and recommends a base with rationale.

Run the judge in parallel with the parent's Phase D reading when the harness allows it. Do not start the judge while candidates are still writing.

## Phase D: Pick a base

Read every candidate end to end before picking.

Score each candidate against the rubric criterion by criterion, not on holistic feel. Compare against the cross-judge. Agreement on the base confirms the pick. Disagreement means one of you is biased or the rubric was ambiguous - read both rationales before deciding.

Pick the base a future maintainer can extend most easily without breaking invariants. When two feel tied, prefer the cleaner boundary or smaller API.

Record the pick and the reason in a short synthesis note alongside the base artifact, including the cross-judge's verdict.

## Phase E: Graft

Walk each losing candidate once more and identify what is worth porting into the base. Usually that is one or two things per candidate, not most of it.

Fold each graft in by hand. Do not paste mechanically. The result has to stay coherent under one mental model.

Record what was grafted, from which candidate, and what was rejected and why.

When N candidates converge on the same shape, that is a strong agreement signal. Note the convergence and ship the consensus shape. No graft is needed. When N candidates wildly diverge, Phase A was under-specified - reframe and re-run rather than averaging the divergence.

## Phase F: Verify

The synthesized artifact has to hold up under the same scrutiny as any other output. Run the checks that prove it works for this task.

If verification surfaces a problem the arena did not catch, either Phase A was wrong (re-frame and re-run) or one candidate caught it and you missed the graft (go back to Phase E). Do not paper over.

## Outputs

One synthesized artifact. One short synthesis note alongside it naming the base, the grafts (with source candidate), the rejections, any dropouts, and the verification result.
