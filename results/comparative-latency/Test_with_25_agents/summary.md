# Comparative Test Summary: comparative-latency

- Run ID: `cmp-20260913-112453`
- Start: 2026-09-13T12:13:55Z
- End: 2026-09-13T14:19:14Z
- Consumer: `consumer-1`
- Broker: https://172.18.0.3:30443
- Providers: 17
- Phase length: 1h0m0s (wall clock; configured iterations ignored)

## Phase A (baseline) — Random

- Iterations: 460 (success: 460, fail: 0)
- Selection distribution:
  - `provider-3`: 31 (6.7%)
  - `provider-10`: 22 (4.8%)
  - `provider-5`: 37 (8.0%)
  - `provider-4`: 26 (5.7%)
  - `provider-15`: 22 (4.8%)
  - `provider-13`: 22 (4.8%)
  - `provider-16`: 27 (5.9%)
  - `provider-17`: 26 (5.7%)
  - `provider-14`: 22 (4.8%)
  - `provider-11`: 28 (6.1%)
  - `provider-12`: 26 (5.7%)
  - `provider-2`: 28 (6.1%)
  - `provider-1`: 31 (6.7%)
  - `provider-6`: 33 (7.2%)
  - `provider-7`: 30 (6.5%)
  - `provider-9`: 26 (5.7%)
  - `provider-8`: 23 (5.0%)
- Mean RTT: 135.10 ms
- Median RTT: 131.60 ms

## Phase B (policy-aware) — Latency

- Iterations: 604 (success: 601, fail: 3)
- Selection distribution:
  - `provider-9`: 108 (17.9%)
  - `provider-3`: 9 (1.5%)
  - `provider-13`: 76 (12.6%)
  - `provider-1`: 90 (14.9%)
  - `provider-14`: 113 (18.7%)
  - `provider-17`: 110 (18.2%)
  - `provider-16`: 95 (15.7%)
- Mean RTT: 71.46 ms
- Median RTT: 60.31 ms

