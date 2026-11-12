# Published runs

The runs the thesis reports, as the harnesses wrote them. Every figure and every number in
the thesis can be rebuilt from here with the scripts in
[federation-tests/scripts/](../federation-tests/scripts/); how to read the files, and what
each column means, is in the [test guide](../federation-tests/guide.md).

## What is in each folder

Run folders are named `Test_with_N_agents`, where `N` is the size of the federation.

| Folder | Runs | What `N` counts |
|---|---|---|
| `comparative-eco/` | Random vs Eco, four scales | agents in the federation: `10` = 3 consumers × 7 providers, `25` = 8×17, `50` = 15×35, `100` = 30×70 |
| `comparative-latency/` | Random vs Latency, the same four scales | as above |
| `scalability/` | Broker load test, eight levels | simulated agents, from `10` (5 consumers + 5 providers) to `1000` (300 + 700) |
| `consumerchoice/` | The LLM policy end to end, all four scenarios | one run, its files kept directly in the folder |

A comparative run holds the CSVs the harness wrote, its `summary.md` / `summary.json`, and an
`analysis/` folder with the figures: PNG, PDF, the CSV behind the figure, a markdown summary
and the `pgfplots` source (`.tex`) the thesis includes.

Beside the runs, not inside one, `comparative-eco/Boxplots/` and
`comparative-latency/Boxplots/` hold the boxplots: they put all four scales in a single
figure, so they belong to no single run. The eco set has both the linear and the logarithmic
version; latency, whose two phases sit on the same order of magnitude, has one.

The scalability runs keep their `broker.log` and `test.log` beside the measurements, so a
level whose numbers look odd can be read against what the broker was doing at the time.

## One thing that is not here

The per-run eco folders hold only the **logarithmic** figure
(`carbon_intensity_comparison_log.*`, `carbon_summary_log.md`). Under Eco the federation runs
an order of magnitude cleaner than under Random, which is why that is the variant the thesis
uses, and the linear one was not committed. It is one command away from the CSVs that are
here — the first command below writes it — so nothing is lost. The latency `analysis/` folders
are complete.

## Rebuilding a figure

```bash
python3 federation-tests/scripts/ecoDiagramMaker.py     --input results/comparative-eco/Test_with_10_agents/reservations.csv
python3 federation-tests/scripts/ecoDiagramMakerLog.py  --input results/comparative-eco/Test_with_10_agents/reservations.csv
python3 federation-tests/scripts/latencyDiagramMaker.py --input results/comparative-latency/Test_with_10_agents/reservations.csv
```

Each writes into the run's own `analysis/` folder. The boxplots take the four runs at once,
and `--output-dir` is what puts them in `Boxplots/` rather than in an `analysis/` folder of
their own:

```bash
python3 federation-tests/scripts/phaseBoxplotMaker.py \
  --output-dir results/comparative-eco/Boxplots \
  results/comparative-eco/Test_with_{10,25,50,100}_agents
```

The band behind the curves is read from `nodegroups.csv` (eco) or `probes.csv` (latency),
which is why both are kept: without them the chart still draws, but only the two curves.

`results/consumerchoice/reasoning.txt` is the one file meant to be read straight through:
every decision as the prompt, the provider chosen, and the model's own account of why. It is
the model's account, never the evidence that a choice was right — that is what the criteria in
`summary.md` are for.

## Checking a run before trusting it

```bash
python3 federation-tests/scripts/verifyReplayAlignment.py        --input results/comparative-eco/Test_with_10_agents/nodegroups.csv
python3 federation-tests/scripts/verifyLatencyReplayAlignment.py --input results/comparative-latency/Test_with_10_agents/probes.csv
```

Both phases must have met the same conditions for the comparison to mean anything; section 9
of the guide explains how to read the verdict.
