# Broker Scalability Test Summary

- Run ID: `20260918T152231Z`
- Start: 2026-09-18T15:22:33Z
- End: 2026-09-18T15:27:33Z
- Measurement-phase duration: 300.0s (configured: 5m0s)
- Logical consumers: 30, logical providers: 70
- Broker URL: https://localhost:9444

## Warm-up

- Providers ready: 70/70 (0.1s)
- Consumers ready: 30/30 (0.0s)

## Measurement-phase traffic

| Operation | Attempts | Success | Fail | Timeout | Cancelled | Mean ms | p50 ms | p95 ms | p99 ms | Success/min | Error rate |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Evaluation (GET /api/v1/nodegroups) | 1800 | 1800 | 0 | 0 | 0 | 5.7 | 5.1 | 10.1 | 11.4 | 360.0 | 0.00% |
| Advertisement (POST /api/v1/advertisements) | 700 | 700 | 0 | 0 | 0 | 12.3 | 12.0 | 13.3 | 16.7 | 140.0 | 0.00% |
| Heartbeat (POST /api/v1/heartbeat) | 600 | 600 | 0 | 0 | 0 | 4.2 | 5.6 | 6.5 | 9.0 | 120.0 | 0.00% |
| Instruction poll (GET /api/v1/instructions) | 6000 | 6000 | 0 | 0 | 0 | 2.2 | 1.1 | 6.0 | 6.5 | 1200.0 | 0.00% |

_Latency percentiles are computed over successful requests only; error rate = (failures+timeouts)/attempts over all measurement-phase requests. Cancelled = stopped by the harness itself (Ctrl+C): not an attempt, not an error. See README.md for why._

## Broker resource usage (--monitor-mode=process)

- Samples: 60
- CPU: avg 0.088, peak 0.222 (cores)
- Memory: avg 69.2 MiB, peak 70.4 MiB
