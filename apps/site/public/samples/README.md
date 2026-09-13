# Sample media provenance

Every file in this directory is original artwork authored directly as hand-written
SVG for this site (flat shapes, gradients and text — no photographs, no
generative-image tooling, no third-party assets). They exist solely to drive
`/demo/`, the in-memory sample presentation.

- `avatar-otterframes.svg`, `avatar-boxcat.svg`, `avatar-ramenroute.svg`,
  `avatar-you.svg` — small abstract avatar marks for the four fictional demo
  accounts.
- `concert-1.svg` / `concert-2.svg` — a two-frame fictional "concert" Story
  sequence (illustrated stage + crowd silhouette, then a text-only follow-up
  card).
- `cat-1.svg` / `cat-2.svg` — a two-frame fictional "cat in a box" Story
  sequence.
- `food-1.svg` / `food-2.svg` — a two-frame fictional "ramen" Story sequence.

None of these depict a real person, a real account, or a real GitHub Stories
post. The demo page repeats this in visible copy: "Sample content. Fictional
accounts. Nothing here is a real person's Story."

These assets are only used by the browser-only in-memory demo. They are not
evidence that the terminal renderer works — that is proved separately by
captured runs of the real `gh stories` CLI against real terminals (see
`mise run capture` and `docs/media/`), not by anything in this directory.
