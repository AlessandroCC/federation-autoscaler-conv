# Comparative Test Summary: comparative-eco

- Run ID: `cmp-20260914-111040`
- Start: 2026-09-14T11:33:10Z
- End: 2026-09-14T13:39:50Z
- Consumer: `consumer-1`
- Broker: https://172.18.0.7:30443
- Providers: 7
- Phase length: 1h0m0s (wall clock; configured iterations ignored)

## Phase A (baseline) — Random

- Iterations: 175 (success: 174, fail: 1)
- Selection distribution:
  - `provider-7`: 21 (12.0%)
  - `provider-5`: 24 (13.7%)
  - `provider-1`: 26 (14.9%)
  - `provider-2`: 28 (16.0%)
  - `provider-4`: 29 (16.6%)
  - `provider-6`: 21 (12.0%)
  - `provider-3`: 25 (14.3%)

## Phase B (policy-aware) — Eco

- Iterations: 256 (success: 256, fail: 0)
- Selection distribution:
  - `provider-6`: 50 (19.5%)
  - `provider-5`: 48 (18.8%)
  - `provider-7`: 25 (9.8%)
  - `provider-3`: 30 (11.7%)
  - `provider-4`: 36 (14.1%)
  - `provider-1`: 39 (15.2%)
  - `provider-2`: 28 (10.9%)
- Mean placement value: 74.0039

