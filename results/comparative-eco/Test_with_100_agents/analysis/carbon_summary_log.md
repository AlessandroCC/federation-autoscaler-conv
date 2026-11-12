# Carbon Intensity Analysis Summary (direct policy comparison)

Phase A and Phase B are each measured from their own start and plotted on the same X axis.

Input: `C:\Users\Aless\OneDrive\Desktop\eco\100\reservations.csv`

- Detected Consumers: 30
- Consumers with at least one valid reservation: 30
- Phase A (Random) duration: 59.84 min (3590.2 s)
- Phase B (Eco) duration: 59.91 min (3594.8 s)
- Valid successful Peered reservation records: 3972
- Ignored failed/incomplete records: 6
- Timeline grid: regular (1 min spacing)

## Aggregate carbon-intensity indicator

- Mean aggregate (Phase A, Random): 12610.34 gCO2eq/kWh
- Mean aggregate (Phase B, Eco): 1668.77 gCO2eq/kWh
- Aggregate difference (A -> B): -10941.57 gCO2eq/kWh (-86.8% decrease from Phase A to Phase B)

## Mean per-active-Consumer carbon intensity

- Phase A (Random): 421.03 gCO2eq/kWh
- Phase B (Eco): 56.21 gCO2eq/kWh
- Per-Consumer difference (A -> B): -364.81 gCO2eq/kWh (-86.6% decrease from Phase A to Phase B)

## Achievable range

At each point in time the range runs from the sum of the x greenest providers in the federation to the sum of the x dirtiest, with x the number of Consumers placing then. It is what the federation made possible, measured from nodegroups.csv.

- Providers summed per point (x): 1–30
- Consumers one provider can host: 2 (read from nodegroups.csv (max_size))
- The curve is the carbon intensity those providers carry **at that moment**, read from the same snapshot as the range, not the value recorded when each Consumer reserved (kept in the CSV as `aggregate_at_choice`). Holding a provider that has turned dirty counts as dirty.
- Mean floor (best possible): 1244.02 gCO2eq/kWh
- Mean ceiling (worst possible): 27421.19 gCO2eq/kWh
- Share of the range captured by Phase A (Random): 56.6%
- Share of the range captured by Phase B (Eco): 98.4%
- 100% means the policy matched the greenest providers available; 0% means it did as badly as the dirtiest. Each phase is scored against its own range, while the shaded band averages the two.
- Largest gap between the two phases' own ranges: 1202.00 gCO2eq/kWh (small means both phases really did face the same federation).

## Warnings

- Logarithmic axis: 3 value(s) fall below the bottom of the frame. They are the first minute of a phase, when only part of the Consumers have peered and the sum is a fraction of its steady-state value; anchoring the axis there would spend most of the figure on empty space. They are all in the CSV.
- Active Consumer count varied over the run: between 1 and 30 (of 30 detected in the file).

## Reproduce

```bash
python3 federation-tests/scripts/ecoDiagramMaker.py --input C:\Users\Aless\OneDrive\Desktop\eco\100\reservations.csv
```
