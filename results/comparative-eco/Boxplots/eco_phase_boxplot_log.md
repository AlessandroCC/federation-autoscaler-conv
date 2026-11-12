# Carbon intensity distribution per phase and scale

One box per phase per scale; the values are per active Consumer, so the scales can be read on one axis. The series are the same ones the per-run charts draw.

| Scale | Phase | Policy | n | Median [gCO2eq/kWh] | Mean [gCO2eq/kWh] | Q1 | Q3 |
|---|---|---|---|---|---|---|---|
| 3x7 | phase-a | Random | 61 | 458.00 | 448.94 | 303.67 | 604.67 |
| 3x7 | phase-b | Eco | 61 | 50.00 | 87.85 | 45.33 | 53.67 |
| 3x7 | floor | Achievable minimum | 62 | 49.67 | 90.74 | 45.33 | 53.67 |
| 8x17 | phase-a | Random | 61 | 413.12 | 411.27 | 296.12 | 535.62 |
| 8x17 | phase-b | Eco | 61 | 42.00 | 51.43 | 39.75 | 46.25 |
| 8x17 | floor | Achievable minimum | 62 | 42.00 | 42.70 | 39.25 | 46.00 |
| 15x35 | phase-a | Random | 61 | 453.73 | 442.24 | 356.33 | 547.60 |
| 15x35 | phase-b | Eco | 61 | 42.00 | 51.10 | 38.87 | 45.13 |
| 15x35 | floor | Achievable minimum | 62 | 41.57 | 41.81 | 38.67 | 44.13 |
| 30x70 | phase-a | Random | 61 | 422.20 | 421.03 | 331.97 | 513.60 |
| 30x70 | phase-b | Eco | 61 | 41.73 | 56.21 | 40.67 | 46.67 |
| 30x70 | floor | Achievable minimum | 62 | 41.20 | 42.04 | 40.53 | 43.67 |

## Gap between the two phases, by scale

- 3x7: median 458.00 -> 50.00 gCO2eq/kWh (-408.00, -89.1%, Eco lower than Random)
- 8x17: median 413.12 -> 42.00 gCO2eq/kWh (-371.12, -89.8%, Eco lower than Random)
- 15x35: median 453.73 -> 42.00 gCO2eq/kWh (-411.73, -90.7%, Eco lower than Random)
- 30x70: median 422.20 -> 41.73 gCO2eq/kWh (-380.47, -90.1%, Eco lower than Random)

## Distance from the best possible, by scale

How far each phase's median sat above the median of what the federation offered at the same moments. A policy on the floor reads 1.00x.

- 3x7: floor 49.67 gCO2eq/kWh — Random 9.22x, Eco 1.01x
- 8x17: floor 42.00 gCO2eq/kWh — Random 9.84x, Eco 1.00x
- 15x35: floor 41.57 gCO2eq/kWh — Random 10.92x, Eco 1.01x
- 30x70: floor 41.20 gCO2eq/kWh — Random 10.25x, Eco 1.01x
