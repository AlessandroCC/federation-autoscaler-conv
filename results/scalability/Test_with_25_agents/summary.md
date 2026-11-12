# Broker Scalability Test Summary

- Run ID: `20260918T150230Z`
- Start: 2026-09-18T15:02:32Z
- End: 2026-09-18T15:07:32Z
- Measurement-phase duration: 300.0s (configured: 5m0s)
- Logical consumers: 8, logical providers: 17
- Broker URL: https://localhost:9444

## Warm-up

- Providers ready: 17/17 (0.0s)
- Consumers ready: 8/8 (0.0s)

## Measurement-phase traffic

| Operation | Attempts | Success | Fail | Timeout | Cancelled | Mean ms | p50 ms | p95 ms | p99 ms | Success/min | Error rate |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Evaluation (GET /api/v1/nodegroups) | 480 | 480 | 0 | 0 | 0 | 2.4 | 2.2 | 4.0 | 6.2 | 96.0 | 0.00% |
| Advertisement (POST /api/v1/advertisements) | 170 | 170 | 0 | 0 | 0 | 12.6 | 12.5 | 14.1 | 16.1 | 34.0 | 0.00% |
| Heartbeat (POST /api/v1/heartbeat) | 160 | 160 | 0 | 0 | 0 | 3.5 | 1.2 | 6.1 | 8.9 | 32.0 | 0.00% |
| Instruction poll (GET /api/v1/instructions) | 1500 | 1500 | 0 | 0 | 0 | 2.3 | 1.1 | 5.8 | 6.2 | 300.0 | 0.00% |

_Latency percentiles are computed over successful requests only; error rate = (failures+timeouts)/attempts over all measurement-phase requests. Cancelled = stopped by the harness itself (Ctrl+C): not an attempt, not an error. See README.md for why._

## Broker resource usage (--monitor-mode=process)

- Samples: 60
- CPU: avg 0.018, peak 0.050 (cores)
- Memory: avg 58.2 MiB, peak 59.2 MiB
