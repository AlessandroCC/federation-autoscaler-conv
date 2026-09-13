# Thesis writing notes

Working scratch space, not part of the code project (see .gitignore). Content here is
drafted and iterated locally, then copy-pasted by hand into the Overleaf project
(`content/chapters/chapterN.tex`), since Overleaf isn't Git-linked.

## Chapter skeleton (from thesis.tex on Overleaf — authoritative, do not rename/reorder without updating there too)

1. Introduction — Context, Problem, Solution, Thesis Structure (~4-5 pages total)
2. Related Work and Background — Technologies (Kubernetes, Liqo, Electricity Map), Related Works
3. Architecture
4. Implementation
5. Experimental Evaluation
6. Conclusions

## Status

| Chapter | Local draft | Status |
|---|---|---|
| 1. Introduction | thesis/chapters/chapter1.tex | not started (write last) |
| 2. Related Work and Background | thesis/chapters/chapter2.tex | not started |
| 3. Architecture | thesis/chapters/chapter3.tex | **draft v1 written** — review needed |
| 4. Implementation | thesis/chapters/chapter4.tex | not started |
| 5. Experimental Evaluation | thesis/chapters/chapter5.tex | blocked — waiting on final results |
| 6. Conclusions | thesis/chapters/chapter6.tex | not started (write last) |

Supervisor-agreed writing order: Architecture → Implementation → (Experimental
Evaluation, once results are ready) → Related Work and Background → Introduction →
Conclusions.

## Chapter 3 (Architecture) — things to resolve before pasting to Overleaf

- Uses `\label{sec:implementation}` at the end, anticipating chapter4's label —
  make sure chapter4.tex actually defines `\label{sec:implementation}` on its
  `\chapter{Implementation}` line, or the cross-reference will show `??`.
- Citations `mid4cc2025`, `liqo`, `kubernetes`, `clusterautoscaler` are not yet in
  bibliography.bib — see thesis/bibliography-notes.md.
- Figures assume `architecture-diagram-2.svg`, `registration-flow.png`,
  `scale-up-execution-flow.png`, `scale-down-execution-flow.png` are uploaded to
  Overleaf's `images/` folder (source: `docs/diagrams/` in this repo).
- No `\gls{}` used (acronyms spelled out inline) — deliberate, to avoid depending on
  glossary entries not yet defined in `glossaries.tex`.
- Tone check requested: technical, "senza spaccare il capello" — kept API/CRD field
  detail, timing constants, and error-handling edge cases out of this chapter on
  purpose; that level of detail is earmarked for chapter 4 (Implementation).
- **v2 (current):** rebalanced per feedback from the supervisor round — Cluster
  Autoscaler is no longer a dedicated subsection or a driving theme (the author
  didn't work on that part of the project directly); it's now a short paragraph
  folded into "gRPC Server". Added a new section, External Service Integration,
  covering the mock-geo/mock-eco → ip-api.com/Electricity Maps swap story, which is
  one of the three pillars the thesis should emphasize (architecture, policy-driven
  provider selection, external service integration). Sprinkled a few `\newline`s
  into the denser paragraphs per request.
- **Open, not yet resolved:** the user said the Overleaf project has no file named
  `thesis.tex` — the main-file name/path assumed in the earlier wiring discussion
  (the `\input{content/chapters/chapterN}` fix) may be wrong. Needs clarifying
  before we can give correct wiring instructions again; doesn't block editing
  chapterN.tex files themselves.

## Open questions / TODO for supervisor

- (add items here as they come up)
