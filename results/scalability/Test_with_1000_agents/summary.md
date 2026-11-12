# Broker Scalability Test Summary

- Run ID: `20260918T162538Z`
- Start: 2026-09-18T16:25:39Z
- End: 2026-09-18T16:30:40Z
- Measurement-phase duration: 300.0s (configured: 5m0s)
- Logical consumers: 300, logical providers: 700
- Broker URL: https://localhost:9444

## Warm-up

- Providers ready: 700/700 (1.4s)
- Consumers ready: 300/300 (0.1s)

## Measurement-phase traffic

| Operation | Attempts | Success | Fail | Timeout | Cancelled | Mean ms | p50 ms | p95 ms | p99 ms | Success/min | Error rate |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Evaluation (GET /api/v1/nodegroups) | 17999 | 17999 | 0 | 0 | 0 | 49.7 | 46.8 | 75.4 | 83.9 | 3599.8 | 0.00% |
| Advertisement (POST /api/v1/advertisements) | 7017 | 7017 | 0 | 0 | 0 | 14.5 | 13.0 | 24.9 | 30.4 | 1403.4 | 0.00% |
| Heartbeat (POST /api/v1/heartbeat) | 6000 | 6000 | 0 | 0 | 0 | 4.6 | 4.4 | 12.2 | 19.3 | 1200.0 | 0.00% |
| Instruction poll (GET /api/v1/instructions) | 60104 | 60104 | 0 | 0 | 0 | 3.1 | 1.2 | 9.6 | 15.9 | 12020.8 | 0.00% |

_Latency percentiles are computed over successful requests only; error rate = (failures+timeouts)/attempts over all measurement-phase requests. Cancelled = stopped by the harness itself (Ctrl+C): not an attempt, not an error. See README.md for why._

## Broker resource usage (--monitor-mode=process)

- Samples: 60
- CPU: avg 4.112, peak 4.286 (cores)
- Memory: avg 150.2 MiB, peak 156.0 MiB
