# Bibliography notes

Candidate references to add to `bibliography.bib` on Overleaf, with why they're relevant.
Verify each before adding — check the actual paper/source, don't trust the citation blind.

## Already in bibliography.bib

- `dougherty2012model` — Dougherty, White, Schmidt, "Model-driven auto-scaling of green cloud
  computing infrastructure", Future Generation Computer Systems, 2012. Cited in chapter2 (Related Works).

## Candidates (not yet added)

Used in chapter3.tex (Architecture) with these keys — not yet in bibliography.bib,
so citations will render as "[?]" until added:

- `mid4cc2025`: "Dynamic Multi-Provider Cluster Autoscaling For The Computing
  Continuum" (Mid4CC '25) — the paper docs/design.md says this project's architecture
  is based on. Need the actual venue/authors/year to write a proper @inproceedings
  entry — check with the supervisor if unpublished/in press.
- `liqo`: Liqo project — cite the project (site/GitHub/paper if one exists:
  liqo.io) for the peering/virtual-node mechanism.
- `kubernetes`: Kubernetes itself — official docs or the original Borg-lineage paper,
  whichever citation style the department expects.
- `clusterautoscaler`: Kubernetes Cluster Autoscaler — project docs/GitHub
  (kubernetes/autoscaler).
- `ipapi`: ip-api.com (IP geolocation service) — mentioned by name in the External
  Service Integration section as the real service the mock-geo stand-in mirrors
  (deliberately *not* used directly — see that section for why). Ready-to-paste
  entry:
  ```bibtex
  @online{ipapi,
    title   = {ip-api.com -- IP Geolocation API},
    author  = {{ip-api.com}},
    url     = {https://ip-api.com},
    urldate = {2026-09-01},
  }
  ```
- `electricitymaps`: Electricity Maps (grid carbon intensity service) — same story,
  mirrored by the mock-eco stand-in. Ready-to-paste entry:
  ```bibtex
  @online{electricitymaps,
    title   = {Electricity Maps},
    author  = {{Electricity Maps}},
    url     = {https://www.electricitymaps.com},
    urldate = {2026-09-01},
  }
  ```

- (add further entries as {bibkey}: short reason, source link)
