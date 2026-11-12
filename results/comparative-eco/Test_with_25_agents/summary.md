# Comparative Test Summary: comparative-eco

- Run ID: `cmp-20260912-180026`
- Start: 2026-09-12T18:50:49Z
- End: 2026-09-12T20:58:37Z
- Consumer: `consumer-1`
- Broker: https://172.18.0.2:30443
- Providers: 17
- Phase length: 1h0m0s (wall clock; configured iterations ignored)

## Phase A (baseline) — Random

- Iterations: 470 (success: 470, fail: 0)
- Selection distribution:
  - `provider-13`: 25 (5.3%)
  - `provider-15`: 22 (4.7%)
  - `provider-14`: 23 (4.9%)
  - `provider-8`: 27 (5.7%)
  - `provider-2`: 31 (6.6%)
  - `provider-4`: 28 (6.0%)
  - `provider-6`: 26 (5.5%)
  - `provider-16`: 32 (6.8%)
  - `provider-7`: 31 (6.6%)
  - `provider-1`: 29 (6.2%)
  - `provider-12`: 32 (6.8%)
  - `provider-17`: 28 (6.0%)
  - `provider-9`: 25 (5.3%)
  - `provider-11`: 25 (5.3%)
  - `provider-10`: 27 (5.7%)
  - `provider-3`: 23 (4.9%)
  - `provider-5`: 36 (7.7%)

## Phase B (policy-aware) — Eco

- Iterations: 649 (success: 647, fail: 2)
- Selection distribution:
  - `provider-5`: 70 (10.8%)
  - `provider-8`: 37 (5.7%)
  - `provider-15`: 39 (6.0%)
  - `provider-9`: 17 (2.6%)
  - `provider-12`: 51 (7.9%)
  - `provider-10`: 62 (9.6%)
  - `provider-11`: 58 (8.9%)
  - `provider-16`: 36 (5.5%)
  - `provider-7`: 33 (5.1%)
  - `provider-14`: 52 (8.0%)
  - `provider-17`: 16 (2.5%)
  - `provider-1`: 26 (4.0%)
  - `provider-2`: 30 (4.6%)
  - `provider-4`: 33 (5.1%)
  - `provider-3`: 26 (4.0%)
  - `provider-13`: 37 (5.7%)
  - `provider-6`: 24 (3.7%)
- Mean placement value: 42.0526

