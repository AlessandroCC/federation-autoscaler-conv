# Broker Scalability Test Summary

- Run ID: `20260918T144747Z`
- Start: 2026-09-18T14:47:48Z
- End: 2026-09-18T14:52:51Z
- Measurement-phase duration: 300.0s (configured: 5m0s)
- Logical consumers: 5, logical providers: 5
- Broker URL: https://localhost:9444

## Warm-up

- Providers ready: 5/5 (2.0s)
- Consumers ready: 5/5 (0.0s)

## Measurement-phase traffic

| Operation | Attempts | Success | Fail | Timeout | Cancelled | Mean ms | p50 ms | p95 ms | p99 ms | Success/min | Error rate |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Evaluation (GET /api/v1/nodegroups) | 300 | 300 | 0 | 0 | 0 | 1.6 | 1.5 | 1.7 | 2.3 | 60.0 | 0.00% |
| Advertisement (POST /api/v1/advertisements) | 50 | 50 | 0 | 0 | 0 | 13.8 | 13.6 | 17.1 | 17.7 | 10.0 | 0.00% |
| Heartbeat (POST /api/v1/heartbeat) | 100 | 100 | 0 | 0 | 0 | 1.1 | 1.1 | 1.2 | 1.3 | 20.0 | 0.00% |
| Instruction poll (GET /api/v1/instructions) | 601 | 601 | 0 | 0 | 0 | 1.0 | 1.0 | 1.1 | 1.2 | 120.2 | 0.00% |

_Latency percentiles are computed over successful requests only; error rate = (failures+timeouts)/attempts over all measurement-phase requests. Cancelled = stopped by the harness itself (Ctrl+C): not an attempt, not an error. See README.md for why._

## Broker resource usage (--monitor-mode=process)

- Samples: 60
- CPU: avg 0.006, peak 0.016 (cores)
- Memory: avg 54.7 MiB, peak 56.1 MiB
