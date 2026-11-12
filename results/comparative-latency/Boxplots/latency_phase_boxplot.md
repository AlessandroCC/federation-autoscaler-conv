# Latency distribution per phase and scale

One box per phase per scale; the values are per active Consumer, so the scales can be read on one axis. The series are the same ones the per-run charts draw.

| Scale | Phase | Policy | n | Median [ms] | Mean [ms] | Q1 | Q3 |
|---|---|---|---|---|---|---|---|
| 3x7 | phase-a | Random | 61 | 128.34 | 135.63 | 112.31 | 162.03 |
| 3x7 | phase-b | Latency | 61 | 70.33 | 72.57 | 55.68 | 83.74 |
| 3x7 | floor | Measured minimum | 62 | 43.73 | 45.68 | 37.89 | 52.27 |
| 8x17 | phase-a | Random | 61 | 132.59 | 136.06 | 123.98 | 149.26 |
| 8x17 | phase-b | Latency | 61 | 73.96 | 73.33 | 63.04 | 82.50 |
| 8x17 | floor | Measured minimum | 62 | 37.92 | 40.15 | 35.01 | 44.56 |
| 15x35 | phase-a | Random | 61 | 139.39 | 138.23 | 130.68 | 148.01 |
| 15x35 | phase-b | Latency | 61 | 72.18 | 73.14 | 65.21 | 78.42 |
| 15x35 | floor | Measured minimum | 62 | 38.32 | 39.64 | 35.76 | 41.47 |
| 30x70 | phase-a | Random | 61 | 141.46 | 140.86 | 133.68 | 149.81 |
| 30x70 | phase-b | Latency | 61 | 75.56 | 76.54 | 72.87 | 80.76 |
| 30x70 | floor | Measured minimum | 62 | 37.06 | 38.10 | 35.75 | 38.79 |

## Gap between the two phases, by scale

- 3x7: median 128.34 -> 70.33 ms (-58.01, -45.2%, Latency lower than Random)
- 8x17: median 132.59 -> 73.96 ms (-58.64, -44.2%, Latency lower than Random)
- 15x35: median 139.39 -> 72.18 ms (-67.21, -48.2%, Latency lower than Random)
- 30x70: median 141.46 -> 75.56 ms (-65.90, -46.6%, Latency lower than Random)

## Distance from the best measured, by scale

How far each phase's median sat above the median of what the federation offered at the same moments. A policy on the floor reads 1.00x.

- 3x7: floor 43.73 ms — Random 2.94x, Latency 1.61x
- 8x17: floor 37.92 ms — Random 3.50x, Latency 1.95x
- 15x35: floor 38.32 ms — Random 3.64x, Latency 1.88x
- 30x70: floor 37.06 ms — Random 3.82x, Latency 2.04x
