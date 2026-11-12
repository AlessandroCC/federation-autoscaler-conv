# Broker Scalability Test Summary

- Run ID: `20260918T150936Z`
- Start: 2026-09-18T15:09:37Z
- End: 2026-09-18T15:14:37Z
- Measurement-phase duration: 300.0s (configured: 5m0s)
- Logical consumers: 15, logical providers: 35
- Broker URL: https://localhost:9444

## Warm-up

- Providers ready: 35/35 (0.0s)
- Consumers ready: 15/15 (0.0s)

## Measurement-phase traffic

| Operation | Attempts | Success | Fail | Timeout | Cancelled | Mean ms | p50 ms | p95 ms | p99 ms | Success/min | Error rate |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Evaluation (GET /api/v1/nodegroups) | 900 | 900 | 0 | 0 | 0 | 3.5 | 3.2 | 6.4 | 7.4 | 180.0 | 0.00% |
| Advertisement (POST /api/v1/advertisements) | 350 | 350 | 0 | 0 | 0 | 12.2 | 12.0 | 13.1 | 22.7 | 70.0 | 0.00% |
| Heartbeat (POST /api/v1/heartbeat) | 301 | 301 | 0 | 0 | 0 | 4.2 | 5.6 | 6.2 | 8.0 | 60.2 | 0.00% |
| Instruction poll (GET /api/v1/instructions) | 3000 | 3000 | 0 | 0 | 0 | 2.2 | 1.1 | 6.0 | 6.4 | 600.0 | 0.00% |

_Latency percentiles are computed over successful requests only; error rate = (failures+timeouts)/attempts over all measurement-phase requests. Cancelled = stopped by the harness itself (Ctrl+C): not an attempt, not an error. See README.md for why._

## Broker resource usage (--monitor-mode=process)

- Samples: 60
- CPU: avg 0.037, peak 0.092 (cores)
- Memory: avg 61.2 MiB, peak 62.2 MiB
