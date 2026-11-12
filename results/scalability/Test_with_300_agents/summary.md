# Broker Scalability Test Summary

- Run ID: `20260918T155712Z`
- Start: 2026-09-18T15:57:13Z
- End: 2026-09-18T16:02:14Z
- Measurement-phase duration: 300.0s (configured: 5m0s)
- Logical consumers: 100, logical providers: 200
- Broker URL: https://localhost:9444

## Warm-up

- Providers ready: 200/200 (0.2s)
- Consumers ready: 100/100 (0.1s)

## Measurement-phase traffic

| Operation | Attempts | Success | Fail | Timeout | Cancelled | Mean ms | p50 ms | p95 ms | p99 ms | Success/min | Error rate |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Evaluation (GET /api/v1/nodegroups) | 6000 | 6000 | 0 | 0 | 0 | 13.2 | 11.8 | 19.8 | 23.0 | 1200.0 | 0.00% |
| Advertisement (POST /api/v1/advertisements) | 2001 | 2001 | 0 | 0 | 0 | 12.4 | 12.1 | 14.4 | 17.9 | 400.2 | 0.00% |
| Heartbeat (POST /api/v1/heartbeat) | 2000 | 2000 | 0 | 0 | 0 | 3.9 | 5.5 | 6.9 | 9.2 | 400.0 | 0.00% |
| Instruction poll (GET /api/v1/instructions) | 18006 | 18006 | 0 | 0 | 0 | 2.4 | 1.1 | 6.2 | 8.0 | 3601.2 | 0.00% |

_Latency percentiles are computed over successful requests only; error rate = (failures+timeouts)/attempts over all measurement-phase requests. Cancelled = stopped by the harness itself (Ctrl+C): not an attempt, not an error. See README.md for why._

## Broker resource usage (--monitor-mode=process)

- Samples: 60
- CPU: avg 0.438, peak 0.866 (cores)
- Memory: avg 85.7 MiB, peak 86.8 MiB
