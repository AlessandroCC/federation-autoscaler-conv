# Broker Scalability Test Summary

- Run ID: `20260918T160609Z`
- Start: 2026-09-18T16:06:11Z
- End: 2026-09-18T16:11:11Z
- Measurement-phase duration: 300.0s (configured: 5m0s)
- Logical consumers: 130, logical providers: 270
- Broker URL: https://localhost:9444

## Warm-up

- Providers ready: 270/270 (0.3s)
- Consumers ready: 130/130 (0.1s)

## Measurement-phase traffic

| Operation | Attempts | Success | Fail | Timeout | Cancelled | Mean ms | p50 ms | p95 ms | p99 ms | Success/min | Error rate |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Evaluation (GET /api/v1/nodegroups) | 7800 | 7800 | 0 | 0 | 0 | 17.6 | 15.7 | 26.2 | 32.3 | 1560.0 | 0.00% |
| Advertisement (POST /api/v1/advertisements) | 2701 | 2701 | 0 | 0 | 0 | 12.5 | 12.2 | 14.7 | 18.5 | 540.2 | 0.00% |
| Heartbeat (POST /api/v1/heartbeat) | 2600 | 2600 | 0 | 0 | 0 | 3.9 | 5.4 | 7.5 | 11.2 | 520.0 | 0.00% |
| Instruction poll (GET /api/v1/instructions) | 24007 | 24007 | 0 | 0 | 0 | 2.5 | 1.1 | 6.3 | 9.5 | 4801.4 | 0.00% |

_Latency percentiles are computed over successful requests only; error rate = (failures+timeouts)/attempts over all measurement-phase requests. Cancelled = stopped by the harness itself (Ctrl+C): not an attempt, not an error. See README.md for why._

## Broker resource usage (--monitor-mode=process)

- Samples: 60
- CPU: avg 0.706, peak 1.176 (cores)
- Memory: avg 95.4 MiB, peak 97.1 MiB
