# ConsumerChoice validation — cmp-20260930-153544

**Verdict: PASS**

- Run: 2026-09-30T15:57:01Z → 2026-09-30T16:15:47Z (18m46s)
- Model: `llama3.2`, queried as the consumer agent queries it (JSON mode, default sampling, timeout 2m0s), fallback `agent-default`
- Scenarios: 4, repetitions each: 5
- Providers: `providers.csv`, the federation every decision was made on; distances from the consumer in Milan (LOM)

## Metrics

| Metric | Value |
|---|---|
| Valid-ID rate | 95.0% (19 of 20 AI calls) |
| Unusable answers, the model's own (timeout, broken JSON, no or invented ID) | 1 of 20 AI calls |
| Reservation success rate | 100.0% (19 Peered of 19 valid AI selections) |
| Prompt-alignment rate | 63.2% (12 of 19 AI selections) |
| Fallback rate | 5.0% (1 of 20 AI calls) |
| Decision latency | mean 20155 ms, p95 34351 ms (n=20) |
| Reservation → Peered | mean 16279 ms, p95 20036 ms (n=20) |

## Scenarios

### eco-oriented

> Prioritize the greenest provider. Latency is secondary.

- Criterion `eco`: 3 of 4 AI selections passed
- Repeatability: 40.0% of 5 AI calls (most frequent `provider-9`), distribution map[provider-4:1 provider-7:1 provider-9:2]

| Rep | Source | Model choice | Final | Carbon | Distance km | Cost/chunk | Ranks C/D/$ | Criterion | Peered | Decision |
|---|---|---|---|---|---|---|---|---|---|---|
| 1 | ai | `provider-9` | `provider-9` | 25 | 6132.9 | 0.082 | 1/8/7 | pass | true (Peered) | 9.919s |
|  |  | reason (not used to judge): _Prioritize the greenest provider. Latency is secondary._ |  |  |  |  |  |  |  |  |
| 2 | ai | `provider-4` | `provider-4` | 350 | 518.1 | 0.052 | 7/3/2 | fail | true (Peered) | 15.878s |
|  |  | reason (not used to judge): _Prioritize the greenest provider_ |  |  |  |  |  |  |  |  |
| 3 | fallback | `` | `provider-8` | 720 | 6472.9 | 0.026 | 9/9/1 | fail | true (Peered) | 2m0.034s |
| 4 | ai | `provider-7` | `provider-7` | 35 | 1936.8 | 0.064 | 2/7/4 | pass | true (Peered) | 8.318s |
|  |  | reason (not used to judge): _Prioritize the greenest provider. Latency is secondary._ |  |  |  |  |  |  |  |  |
| 5 | ai | `provider-9` | `provider-9` | 25 | 6132.9 | 0.082 | 1/8/7 | pass | true (Peered) | 10.99s |
|  |  | reason (not used to judge): _Prioritize the greenest provider. Latency is secondary._ |  |  |  |  |  |  |  |  |

### proximity-oriented

> Choose a provider close to me, and preferably with low emissions.

- Criterion `proximity`: 1 of 5 AI selections passed
- Repeatability: 60.0% of 5 AI calls (most frequent `provider-4`), distribution map[provider-2:1 provider-3:1 provider-4:3]

| Rep | Source | Model choice | Final | Carbon | Distance km | Cost/chunk | Ranks C/D/$ | Criterion | Peered | Decision |
|---|---|---|---|---|---|---|---|---|---|---|
| 1 | ai | `provider-3` | `provider-3` | 60 | 639.6 | 0.09 | 4/5/8 | fail | true (Peered) | 12.266s |
|  |  | reason (not used to judge): _Choose a provider close to me, and preferably with low emissions._ |  |  |  |  |  |  |  |  |
| 2 | ai | `provider-2` | `provider-2` | 45 | 218.4 | 0.113 | 3/2/9 | pass | true (Peered) | 34.351s |
|  |  | reason (not used to judge): _Choose a provider close to me, and preferably with low emissions._ |  |  |  |  |  |  |  |  |
| 3 | ai | `provider-4` | `provider-4` | 350 | 518.1 | 0.052 | 7/3/2 | fail | true (Peered) | 6.93s |
|  |  | reason (not used to judge): _Choose a provider close to me, and preferably with low emissions._ |  |  |  |  |  |  |  |  |
| 4 | ai | `provider-4` | `provider-4` | 350 | 518.1 | 0.052 | 7/3/2 | fail | true (Peered) | 15.48s |
|  |  | reason (not used to judge): _Choose a provider close to you, and preferably with low emissions._ |  |  |  |  |  |  |  |  |
| 5 | ai | `provider-4` | `provider-4` | 350 | 518.1 | 0.052 | 7/3/2 | fail | true (Peered) | 27.17s |
|  |  | reason (not used to judge): _Choose a provider close to me, and preferably with low emissions._ |  |  |  |  |  |  |  |  |

### balanced

> I need a good balance between low emissions and proximity.

- Criterion `balanced`: 3 of 5 AI selections passed
- Repeatability: 40.0% of 5 AI calls (most frequent `provider-3`), distribution map[provider-2:1 provider-3:2 provider-8:2]

| Rep | Source | Model choice | Final | Carbon | Distance km | Cost/chunk | Ranks C/D/$ | Criterion | Peered | Decision |
|---|---|---|---|---|---|---|---|---|---|---|
| 1 | ai | `provider-8` | `provider-8` | 720 | 6472.9 | 0.026 | 9/9/1 | fail | true (Peered) | 7.776s |
|  |  | reason (not used to judge): _balance between low emissions and proximity_ |  |  |  |  |  |  |  |  |
| 2 | ai | `provider-8` | `provider-8` | 720 | 6472.9 | 0.026 | 9/9/1 | fail | true (Peered) | 12.542s |
|  |  | reason (not used to judge): _find the provider with a good balance between low emissions and proximity_ |  |  |  |  |  |  |  |  |
| 3 | ai | `provider-3` | `provider-3` | 60 | 639.6 | 0.09 | 4/5/8 | pass | true (Peered) | 18.715s |
|  |  | reason (not used to judge): _good balance between low emissions and proximity_ |  |  |  |  |  |  |  |  |
| 4 | ai | `provider-2` | `provider-2` | 45 | 218.4 | 0.113 | 3/2/9 | pass | true (Peered) | 15.009s |
|  |  | reason (not used to judge): _balance between low emissions and proximity_ |  |  |  |  |  |  |  |  |
| 5 | ai | `provider-3` | `provider-3` | 60 | 639.6 | 0.09 | 4/5/8 | pass | true (Peered) | 15.199s |
|  |  | reason (not used to judge): _balance between low emissions and proximity_ |  |  |  |  |  |  |  |  |

### ambiguous

> Choose the best provider for my workload.

- Criterion `none`: 5 of 5 AI selections passed
- Repeatability: 100.0% of 5 AI calls (most frequent `provider-8`), distribution map[provider-8:5]

| Rep | Source | Model choice | Final | Carbon | Distance km | Cost/chunk | Ranks C/D/$ | Criterion | Peered | Decision |
|---|---|---|---|---|---|---|---|---|---|---|
| 1 | ai | `provider-8` | `provider-8` | 720 | 6472.9 | 0.026 | 9/9/1 | pass | true (Peered) | 10.189s |
|  |  | reason (not used to judge): _Choose the best provider for my workload._ |  |  |  |  |  |  |  |  |
| 2 | ai | `provider-8` | `provider-8` | 720 | 6472.9 | 0.026 | 9/9/1 | pass | true (Peered) | 14.307s |
|  |  | reason (not used to judge): _Choose the best provider for my workload._ |  |  |  |  |  |  |  |  |
| 3 | ai | `provider-8` | `provider-8` | 720 | 6472.9 | 0.026 | 9/9/1 | pass | true (Peered) | 10.825s |
|  |  | reason (not used to judge): _Choose the best provider for my workload._ |  |  |  |  |  |  |  |  |
| 4 | ai | `provider-8` | `provider-8` | 720 | 6472.9 | 0.026 | 9/9/1 | pass | true (Peered) | 16.412s |
|  |  | reason (not used to judge): _Choose the best provider for my workload._ |  |  |  |  |  |  |  |  |
| 5 | ai | `provider-8` | `provider-8` | 720 | 6472.9 | 0.026 | 9/9/1 | pass | true (Peered) | 20.782s |
|  |  | reason (not used to judge): _lowest cost_ |  |  |  |  |  |  |  |  |

## Agent path

A manual reservation per scenario, placed by the consumer agent itself: its local API asked the model and masked the list; the harness only asked for capacity.

| Scenario | Source | Agent's choice | Reserved | Phase | Active after | Criterion | Passed |
|---|---|---|---|---|---|---|---|
| eco-oriented | ai | `provider-6` | `provider-6` | Active | 45s | fail | yes |
| proximity-oriented | ai | `provider-7` | `provider-7` | Active | 30s | fail | yes |
| balanced | ai | `provider-3` | `provider-3` | Active | 30s | pass | yes |
| ambiguous | ai | `provider-8` | `provider-8` | Active | 45s | pass | yes |

## Failures

| Category | Count |
|---|---|
| timeout | 1 |

## Reading these results

This is a functional validation of AI-assisted selection, not a benchmark of the model. Ranks are computed among the eligible providers the model was shown (1 = lowest value). A criterion pass is evidence that the choice is consistent with the request under a transparent rule; it is not a claim that the choice was optimal.
