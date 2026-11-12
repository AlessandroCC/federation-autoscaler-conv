# Comparative Test Summary: comparative-latency

- Run ID: `cmp-20260914-110852`
- Start: 2026-09-14T11:31:20Z
- End: 2026-09-14T13:35:12Z
- Consumer: `consumer-1`
- Broker: https://172.18.0.2:30443
- Providers: 7
- Phase length: 1h0m0s (wall clock; configured iterations ignored)

## Phase A (baseline) — Random

- Iterations: 172 (success: 172, fail: 0)
- Selection distribution:
  - `provider-5`: 32 (18.6%)
  - `provider-4`: 26 (15.1%)
  - `provider-3`: 26 (15.1%)
  - `provider-6`: 22 (12.8%)
  - `provider-1`: 14 (8.1%)
  - `provider-7`: 26 (15.1%)
  - `provider-2`: 26 (15.1%)
- Mean RTT: 136.82 ms
- Median RTT: 137.68 ms

## Phase B (policy-aware) — Latency

- Iterations: 235 (success: 235, fail: 0)
- Selection distribution:
  - `provider-5`: 45 (19.1%)
  - `provider-1`: 49 (20.9%)
  - `provider-4`: 36 (15.3%)
  - `provider-3`: 33 (14.0%)
  - `provider-6`: 37 (15.7%)
  - `provider-7`: 35 (14.9%)
- Mean RTT: 69.48 ms
- Median RTT: 57.67 ms

