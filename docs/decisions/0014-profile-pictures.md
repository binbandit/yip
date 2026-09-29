# 0014 — Profile pictures for the owner and engineers

**Decision.** At the owner's request (29 September 2026), the owner can set a
profile picture for themselves and for each engineer: PNG, JPEG, GIF
(animated too) or WebP, up to 10 MB. This reinterprets the message-anatomy
line in `02-product-and-design.md` §8, "Avoid fake photographs or presence
that implies a human is online".

**Why.** The owner asked to "change profile pictures of themself, and the
bots", with GIFs allowed. A picture is chosen by the owner, in their own
workspace, for identities they already know are engineers; yip never
supplies or generates a photograph.

**What still keeps engineers identifiable.**

- Shape stays the only human/engineer distinction and doesn't change with a
  picture: the owner is a circle, engineers are rounded squares.
- Profiles, roles and accessible identity labels are unchanged; avatars stay
  hidden from assistive tech beside the name they belong to.
- No presence: a picture never carries an online dot or an "active" state.

**What still applies.** Reduced motion (spec §5 motion): when it's
preferred, every picture shows its first frame, so an animated GIF stands
still. The hub reads each upload's format from its bytes and refuses
anything else; SVG is never accepted because it can carry script.

**Consequences.** Pictures are artifacts of kind `avatar`, so backups,
restores and exports carry them. An engineer's picture isn't configuration:
it writes no new configuration version and running work is unaffected.
