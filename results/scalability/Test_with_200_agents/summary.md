# Broker Scalability Test Summary

- Run ID: `20260918T154935Z`
- Start: 2026-09-18T15:49:36Z
- End: 2026-09-18T15:54:36Z
- Measurement-phase duration: 300.0s (configured: 5m0s)
- Logical consumers: 60, logical providers: 140
- Broker URL: https://localhost:9444

## Warm-up

- Providers ready: 140/140 (0.2s)
- Consumers ready: 60/60 (0.0s)

## Measurement-phase traffic

| Operation | Attempts | Success | Fail | Timeout | Cancelled | Mean ms | p50 ms | p95 ms | p99 ms | Success/min | Error rate |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Evaluation (GET /api/v1/nodegroups) | 3600 | 3600 | 0 | 0 | 0 | 9.7 | 8.7 | 15.3 | 17.1 | 720.0 | 0.00% |
| Advertisement (POST /api/v1/advertisements) | 1400 | 1400 | 0 | 0 | 0 | 12.2 | 12.0 | 13.3 | 16.8 | 280.0 | 0.00% |
| Heartbeat (POST /api/v1/heartbeat) | 1200 | 1200 | 0 | 0 | 0 | 3.6 | 3.9 | 6.4 | 10.2 | 240.0 | 0.00% |
| Instruction poll (GET /api/v1/instructions) | 12001 | 12001 | 0 | 0 | 0 | 2.3 | 1.1 | 6.1 | 7.6 | 2400.2 | 0.00% |

_Latency percentiles are computed over successful requests only; error rate = (failures+timeouts)/attempts over all measurement-phase requests. Cancelled = stopped by the harness itself (Ctrl+C): not an attempt, not an error. See README.md for why._

## Broker resource usage (--monitor-mode=process)

- Samples: 60
- CPU: avg 0.221, peak 0.480 (cores)
- Memory: avg 78.1 MiB, peak 78.9 MiB
