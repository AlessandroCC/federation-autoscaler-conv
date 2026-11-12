# Comparative Test Summary: comparative-latency

- Run ID: `cmp-20260913-153244`
- Start: 2026-09-13T17:15:47Z
- End: 2026-09-13T19:23:03Z
- Consumer: `consumer-1`
- Broker: https://172.18.0.2:30443
- Providers: 35
- Phase length: 1h0m0s (wall clock; configured iterations ignored)

## Phase A (baseline) — Random

- Iterations: 853 (success: 853, fail: 0)
- Selection distribution:
  - `provider-35`: 25 (2.9%)
  - `provider-13`: 22 (2.6%)
  - `provider-27`: 24 (2.8%)
  - `provider-32`: 22 (2.6%)
  - `provider-8`: 26 (3.0%)
  - `provider-7`: 23 (2.7%)
  - `provider-11`: 21 (2.5%)
  - `provider-29`: 21 (2.5%)
  - `provider-15`: 31 (3.6%)
  - `provider-1`: 23 (2.7%)
  - `provider-10`: 25 (2.9%)
  - `provider-3`: 20 (2.3%)
  - `provider-26`: 22 (2.6%)
  - `provider-20`: 24 (2.8%)
  - `provider-33`: 19 (2.2%)
  - `provider-5`: 22 (2.6%)
  - `provider-24`: 28 (3.3%)
  - `provider-21`: 22 (2.6%)
  - `provider-25`: 23 (2.7%)
  - `provider-6`: 23 (2.7%)
  - `provider-34`: 25 (2.9%)
  - `provider-16`: 20 (2.3%)
  - `provider-18`: 29 (3.4%)
  - `provider-28`: 30 (3.5%)
  - `provider-2`: 22 (2.6%)
  - `provider-9`: 38 (4.5%)
  - `provider-17`: 29 (3.4%)
  - `provider-30`: 19 (2.2%)
  - `provider-31`: 24 (2.8%)
  - `provider-19`: 22 (2.6%)
  - `provider-22`: 27 (3.2%)
  - `provider-23`: 28 (3.3%)
  - `provider-12`: 24 (2.8%)
  - `provider-4`: 27 (3.2%)
  - `provider-14`: 23 (2.7%)
- Mean RTT: 139.51 ms
- Median RTT: 139.69 ms

## Phase B (policy-aware) — Latency

- Iterations: 1097 (success: 1094, fail: 3)
- Selection distribution:
  - `provider-32`: 118 (10.8%)
  - `provider-10`: 25 (2.3%)
  - `provider-20`: 82 (7.5%)
  - `provider-1`: 129 (11.8%)
  - `provider-27`: 126 (11.5%)
  - `provider-11`: 125 (11.4%)
  - `provider-12`: 118 (10.8%)
  - `provider-9`: 127 (11.6%)
  - `provider-25`: 122 (11.1%)
  - `provider-28`: 122 (11.1%)
- Mean RTT: 70.00 ms
- Median RTT: 60.60 ms

