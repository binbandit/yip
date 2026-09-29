# Web test fixtures

These are hub API payloads and an SSE stream recorded from a hub whose
engineers followed a fixed script instead of running a real provider. The
hub's side (IDs, states, versions, event order) is what a hub produces; the
engineers' words, tool calls and timings are scripted. Provider fields were
later rewritten to `codex` when the scripted provider was removed, so they
don't describe a real Codex session.

Use them to test how the client renders and reduces hub payloads, not as
evidence of how any provider behaves. When a payload shape changes, edit the
affected fixtures by hand to match `src/lib/api/types.gen.ts`.
