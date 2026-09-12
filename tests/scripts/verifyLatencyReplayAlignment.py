#!/usr/bin/env python3
"""Check that Phase B observed the same latency environment Phase A did.

This is the latency counterpart of verifyReplayAlignment.py, and it exists as a
SEPARATE script rather than a shared code path because the two measurements are
not the same kind of thing.

comparative-eco writes a carbon-intensity number and the Consumer later reads
back that same number -- an exact match is the right question to ask. Here,
what's replayed deterministically is the injected tc delay, but what a
Consumer measures is the round-trip time over a real UDP socket: injected
delay plus whatever jitter the kernel and the shared host add on top. Two
probes against the identical tc delay do not return the identical RTT. Asking
for an exact match would therefore fail even on a perfectly-aligned run, so
this script buckets RTTs to a millisecond tolerance before comparing -- coarse
enough to absorb real jitter, fine enough that two different delay draws
(30-250ms apart per config) still land in different buckets.

Method: same tick-binning and chance-baseline idea as the eco script (see it
for the fuller rationale) but applied to nodegroups.csv... no -- to probes.csv,
keyed by (consumer, provider) rather than provider alone, since each Consumer
has its own prober and its own tc delay to each provider.

Usage:
    python verifyLatencyReplayAlignment.py --input results/.../probes.csv
    python verifyLatencyReplayAlignment.py --input ... --tick-seconds 120 --tolerance-ms 5
"""

from __future__ import annotations

import argparse
import csv
import random
import statistics
import sys
from collections import Counter, defaultdict
from datetime import datetime, timedelta
from pathlib import Path

PHASE_A = "phase-a"
PHASE_B = "phase-b"

PASS_THRESHOLD = 80.0
FAIL_THRESHOLD = 50.0

REQUIRED_COLUMNS = ["timestamp", "consumer_id", "phase", "provider_id", "rtt_ms"]


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    p = argparse.ArgumentParser(
        description="Verify Phase B observed the same latency environment as Phase A.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__,
    )
    p.add_argument("--input", required=True, type=Path, help="Path to a run's probes.csv.")
    p.add_argument(
        "--tick-seconds",
        type=float,
        default=120.0,
        help="latencyRefreshInterval of the run, in seconds (default: 120, i.e. 2m).",
    )
    p.add_argument(
        "--tolerance-ms",
        type=float,
        default=5.0,
        help="Bucket width for RTT comparison, in ms (default: 5). Widen it if the "
        "run's host is noisy and matches come out lower than they should.",
    )
    p.add_argument("--windows", type=int, default=6, help="Windows to split the phase into (default: 6).")
    p.add_argument("--shuffles", type=int, default=200, help="Chance-baseline shuffles (default: 200).")
    return p.parse_args(argv)


def parse_timestamp(raw: str) -> datetime | None:
    s = raw.strip().replace("Z", "+00:00")
    if not s:
        return None
    if "." in s:
        head, rest = s.split(".", 1)
        frac, _, tz = rest.partition("+")
        s = f"{head}.{frac[:6].ljust(6, '0')}+{tz}" if tz else f"{head}.{frac[:6].ljust(6, '0')}"
    try:
        return datetime.fromisoformat(s)
    except ValueError:
        return None


def load_observations(path: Path) -> tuple[list[tuple[str, str, str, datetime, float]], dict[str, datetime]]:
    """Return (phase, consumer, provider, timestamp, rtt_ms) rows plus phase starts."""
    if not path.is_file():
        sys.exit(f"error: no such file: {path}")

    rows: list[tuple[str, str, str, datetime, float]] = []
    starts: dict[str, datetime] = {}

    with path.open(newline="", encoding="utf-8") as f:
        reader = csv.DictReader(f)
        if reader.fieldnames is None:
            sys.exit(f"error: {path} is empty")
        missing = [c for c in REQUIRED_COLUMNS if c not in reader.fieldnames]
        if missing:
            sys.exit(
                f"error: {path} is missing required column(s): {', '.join(missing)}. "
                "This script reads probes.csv, not nodegroups.csv -- use verifyReplayAlignment.py "
                "for the eco test's carbon-intensity check."
            )

        for row in reader:
            phase = row.get("phase", "")
            if phase not in (PHASE_A, PHASE_B):
                continue
            ts = parse_timestamp(row.get("timestamp", ""))
            if ts is None:
                continue
            raw_rtt = (row.get("rtt_ms") or "").strip()
            try:
                rtt = float(raw_rtt)
            except ValueError:
                continue
            # An unreachable provider is recorded as +Inf (see prober.Result):
            # meaningful as "this provider timed out", not as a delay value to
            # bucket and compare.
            if rtt in (float("inf"), float("-inf")):
                continue
            starts[phase] = min(starts.get(phase, ts), ts)
            rows.append((phase, row.get("consumer_id", ""), row.get("provider_id", ""), ts, rtt))

    return rows, starts


def bin_by_tick(
    rows: list[tuple[str, str, str, datetime, float]],
    starts: dict[str, datetime],
    tick: float,
    tolerance_ms: float,
) -> dict[tuple[str, str, str, int], Counter]:
    bins: dict[tuple[str, str, str, int], Counter] = defaultdict(Counter)
    for phase, consumer, provider, ts, rtt in rows:
        elapsed = (ts - starts[phase]).total_seconds()
        bucket = round(rtt / tolerance_ms)
        bins[(phase, consumer, provider, int(elapsed // tick))][bucket] += 1
    return bins


def modal(bins, phase: str, consumer: str, provider: str, k: int) -> int | None:
    c = bins.get((phase, consumer, provider, k))
    return c.most_common(1)[0][0] if c else None


def compare(bins, pairs: list[tuple[str, str]], ticks: range) -> tuple[int, int]:
    same = total = 0
    for consumer, provider in pairs:
        for k in ticks:
            a = modal(bins, PHASE_A, consumer, provider, k)
            b = modal(bins, PHASE_B, consumer, provider, k)
            if a is None or b is None:
                continue
            total += 1
            same += a == b
    return same, total


def chance_baseline(bins, pairs: list[tuple[str, str]], ticks: range, shuffles: int) -> tuple[float, float]:
    """Match rate with Phase B's provider labels shuffled per consumer."""
    rng = random.Random(0)
    by_consumer: dict[str, list[str]] = defaultdict(list)
    for consumer, provider in pairs:
        by_consumer[consumer].append(provider)

    rates: list[float] = []
    for _ in range(shuffles):
        mapping: dict[tuple[str, str], str] = {}
        for consumer, providers in by_consumer.items():
            shuffled = providers[:]
            rng.shuffle(shuffled)
            for provider, swapped in zip(providers, shuffled):
                mapping[(consumer, provider)] = swapped

        same = total = 0
        for consumer, provider in pairs:
            swapped = mapping[(consumer, provider)]
            for k in ticks:
                a = modal(bins, PHASE_A, consumer, provider, k)
                b = modal(bins, PHASE_B, consumer, swapped, k)
                if a is None or b is None:
                    continue
                total += 1
                same += a == b
        if total:
            rates.append(same / total * 100)
    if not rates:
        return float("nan"), float("nan")
    return statistics.mean(rates), statistics.pstdev(rates)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    tick = args.tick_seconds
    if tick <= 0:
        sys.exit("error: --tick-seconds must be positive")
    if args.tolerance_ms <= 0:
        sys.exit("error: --tolerance-ms must be positive")

    rows, starts = load_observations(args.input)
    if PHASE_A not in starts or PHASE_B not in starts:
        sys.exit("error: the file does not contain both phase-a and phase-b RTT observations")

    bins = bin_by_tick(rows, starts, tick, args.tolerance_ms)
    pairs = sorted({(c, p) for _, c, p, _, _ in rows})

    def last_tick(phase: str) -> int:
        ks = [k for (ph, _, _, k) in bins if ph == phase]
        return max(ks) if ks else -1

    shared = min(last_tick(PHASE_A), last_tick(PHASE_B))
    if shared < 0:
        sys.exit("error: no overlapping ticks between the two phases")
    ticks = range(shared + 1)

    same, total = compare(bins, pairs, ticks)
    if not total:
        sys.exit("error: no tick had an observation in both phases for the same (consumer, provider)")
    match = same / total * 100
    baseline, spread = chance_baseline(bins, pairs, ticks, args.shuffles)

    span_a = (starts[PHASE_B] - starts[PHASE_A]).total_seconds()
    print(f"Input:              {args.input}")
    print(f"Tick length:        {tick:.0f}s ({timedelta(seconds=tick)})")
    print(f"Bucket tolerance:   +/-{args.tolerance_ms:.1f}ms")
    print(f"(consumer,provider) pairs: {len(pairs)}")
    print(f"Ticks compared:     {shared + 1} (phase A start to phase B start: {span_a / 60:.1f} min)")
    print()
    print(f"Per-tick match:     {match:.1f}%  ({same}/{total})")
    print(f"Chance baseline:    {baseline:.1f}% +/- {spread:.1f}%")
    print()

    if args.windows > 1 and shared + 1 >= args.windows:
        per = (shared + 1) / args.windows
        print("Per-window match (to catch progressive drift):")
        for w in range(args.windows):
            lo, hi = int(w * per), int((w + 1) * per)
            s, t = compare(bins, pairs, range(lo, hi))
            label = f"ticks {lo:>3}-{hi - 1:<3}"
            print(f"  {label}  {s / t * 100:5.1f}%  ({s}/{t})" if t else f"  {label}      n/a")
        print()

    if match >= PASS_THRESHOLD:
        print(f"PASS: the phases were observed as the same latency environment (>= {PASS_THRESHOLD:.0f}%).")
        return 0
    if match >= FAIL_THRESHOLD:
        print(
            f"INCONCLUSIVE: {match:.1f}% is above chance but below the {PASS_THRESHOLD:.0f}% bar.\n"
            "Try widening --tolerance-ms before concluding the run is misaligned -- real jitter on a\n"
            "loaded host can push a genuinely-aligned run below this bar at a tight tolerance."
        )
        return 2
    print(
        f"FAIL: {match:.1f}% -- Phase A and Phase B were not observed as the same latency environment.\n"
        "Check that the observation lag (the Consumer's 15s prober cache) is small against\n"
        "latencyRefreshInterval; the harness logs a warning at startup when it is not."
    )
    return 1


if __name__ == "__main__":
    sys.exit(main())
