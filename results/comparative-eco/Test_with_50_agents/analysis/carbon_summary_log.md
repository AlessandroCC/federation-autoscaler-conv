# Carbon Intensity Analysis Summary (direct policy comparison)

Phase A and Phase B are each measured from their own start and plotted on the same X axis.

Input: `C:\Users\Aless\OneDrive\Desktop\eco\50\reservations.csv`

- Detected Consumers: 15
- Consumers with at least one valid reservation: 15
- Phase A (Random) duration: 59.92 min (3595.3 s)
- Phase B (Eco) duration: 60.00 min (3599.9 s)
- Valid successful Peered reservation records: 2039
- Ignored failed/incomplete records: 4
- Timeline grid: regular (1 min spacing)

## Aggregate carbon-intensity indicator

- Mean aggregate (Phase A, Random): 6618.93 gCO2eq/kWh
- Mean aggregate (Phase B, Eco): 758.20 gCO2eq/kWh
- Aggregate difference (A -> B): -5860.74 gCO2eq/kWh (-88.5% decrease from Phase A to Phase B)

## Mean per-active-Consumer carbon intensity

- Phase A (Random): 442.24 gCO2eq/kWh
- Phase B (Eco): 51.10 gCO2eq/kWh
- Per-Consumer difference (A -> B): -391.14 gCO2eq/kWh (-88.4% decrease from Phase A to Phase B)

## Achievable range

At each point in time the range runs from the sum of the x greenest providers in the federation to the sum of the x dirtiest, with x the number of Consumers placing then. It is what the federation made possible, measured from nodegroups.csv.

- Providers summed per point (x): 1–15
- Consumers one provider can host: 2 (read from nodegroups.csv (max_size))
- The curve is the carbon intensity those providers carry **at that moment**, read from the same snapshot as the range, not the value recorded when each Consumer reserved (kept in the CSV as `aggregate_at_choice`). Holding a provider that has turned dirty counts as dirty.
- Mean floor (best possible): 619.25 gCO2eq/kWh
- Mean ceiling (worst possible): 13631.88 gCO2eq/kWh
- Share of the range captured by Phase A (Random): 53.8%
- Share of the range captured by Phase B (Eco): 98.9%
- 100% means the policy matched the greenest providers available; 0% means it did as badly as the dirtiest. Each phase is scored against its own range, while the shaded band averages the two.
- Largest gap between the two phases' own ranges: 922.00 gCO2eq/kWh (small means both phases really did face the same federation).

## Warnings

- Logarithmic axis: 3 value(s) fall below the bottom of the frame. They are the first minute of a phase, when only part of the Consumers have peered and the sum is a fraction of its steady-state value; anchoring the axis there would spend most of the figure on empty space. They are all in the CSV.
- Active Consumer count varied over the run: between 1 and 15 (of 15 detected in the file).

## Reproduce

```bash
python3 federation-tests/scripts/ecoDiagramMaker.py --input C:\Users\Aless\OneDrive\Desktop\eco\50\reservations.csv
```
