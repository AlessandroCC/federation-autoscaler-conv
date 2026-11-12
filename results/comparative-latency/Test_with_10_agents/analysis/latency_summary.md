# Latency Analysis Summary (direct policy comparison)

Phase A and Phase B are each measured from their own start and plotted on the same X axis. The indicator is the mean RTT from each active Consumer to the provider it is peered with.

Input: `C:\Users\Aless\OneDrive\Desktop\latency\10\reservations.csv`

- Detected Consumers: 3
- Consumers with at least one valid reservation: 3
- Phase A (Random) duration: 59.58 min (3574.8 s)
- Phase B (Latency) duration: 59.95 min (3597.0 s)
- Valid successful Peered reservation records with an RTT: 407
- Ignored records (failed, incomplete, or without an RTT): 0
- Timeline grid: regular (1 min spacing)

## Mean RTT to the selected provider

- Phase A (Random): 135.63 ms
- Phase B (Latency): 72.57 ms
- Difference (A -> B): -63.07 ms (-46.5%; Phase B lower, i.e. closer providers)

## Observed RTT range

At each point in time the range runs from the mean of the x fastest RTTs measured anywhere in the federation to the mean of the x slowest, with x the number of Consumers placing then.

- RTTs averaged per edge (x): 1–3
- Mean floor (fastest measured): 45.68 ms
- Mean ceiling (slowest measured): 234.77 ms
- Share of the range captured by Phase A (Random): 52.5%
- Share of the range captured by Phase B (Latency): 85.6%
- The curve is each Consumer's RTT as measured when it peered, while the range is built from every probe of the refresh window a point falls in: the two read the same environment a few tens of seconds apart, so a curve outside the range is possible in principle. Points where that happens: 0.
- Mean coverage: 79.0% of the 21 Consumer-provider pairs were measured per point

**Read this range as a floor on what was possible, not the real optimum.** The simulated delays are not recorded anywhere, so only the pairs the Consumers actually probed can be ranked: under Random that is the one provider the Broker masked to, under Latency the three nearest. The fastest pair that nobody probed is missing from the floor, which therefore sits higher than the true best, and the policy looks closer to optimal than it is. Exporting the tc delay matrix from the harness and re-running would remove the caveat.

## Warnings

- phase-a had too few value changes of its own to locate the refresh boundaries, so it uses the other phase's estimate. Both phases run the same schedule, which is what the replay rests on, but a range there is only as good as that assumption.
- Active Consumer count varied over the run: between 1 and 3 (of 3 detected in the file). The mean is taken over active Consumers only, so this changes how many readings each point averages, not its scale.

## Reproduce

```bash
python3 federation-tests/scripts/latencyDiagramMaker.py --input C:\Users\Aless\OneDrive\Desktop\latency\10\reservations.csv
```
