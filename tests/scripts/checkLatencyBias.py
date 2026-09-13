#!/usr/bin/env python3
"""One-off diagnostic: is Phase A vs Phase B RTT a systematic shift or scattered noise?

Not part of the regular verification suite -- run this only when
verifyLatencyReplayAlignment.py comes back inconclusive/failing even at a wide
--tolerance-ms, to tell apart two very different explanations:

  - a consistent shift (most pairs show B higher than A, or vice versa, by a
    similar amount): points to a systemic RTT difference between phases (e.g.
    Phase A's heavier Random-switching churn loading the host differently than
    Phase B's steadier state) rather than a broken delay replay.
  - scattered, sign-flipping differences with no common direction: points away
    from a load confound and back toward a real alignment problem worth
    digging into (wrong seed, wrong reset, etc).

Usage:
    python checkLatencyBias.py --input results/.../probes.csv
"""

from __future__ import annotations

import argparse
import csv
import statistics
import sys
from collections import defaultdict
from pathlib import Path

PHASE_A = "phase-a"
PHASE_B = "phase-b"


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--input", required=True, type=Path)
    args = p.parse_args()

    rtts: dict[tuple[str, str, str], list[float]] = defaultdict(list)
    with args.input.open(newline="", encoding="utf-8") as f:
        for row in csv.DictReader(f):
            phase = row.get("phase", "")
            if phase not in (PHASE_A, PHASE_B):
                continue
            try:
                rtt = float(row["rtt_ms"])
            except (KeyError, ValueError):
                continue
            if rtt in (float("inf"), float("-inf")):
                continue
            rtts[(phase, row["consumer_id"], row["provider_id"])].append(rtt)

    pairs = sorted({(c, p) for _, c, p in rtts})
    diffs = []
    print(f"{'consumer':<12} {'provider':<12} {'mean A':>9} {'mean B':>9} {'B-A':>8}")
    for consumer, provider in pairs:
        a = rtts.get((PHASE_A, consumer, provider))
        b = rtts.get((PHASE_B, consumer, provider))
        if not a or not b:
            continue
        ma, mb = statistics.mean(a), statistics.mean(b)
        diffs.append(mb - ma)
        print(f"{consumer:<12} {provider:<12} {ma:9.2f} {mb:9.2f} {mb - ma:8.2f}")

    if not diffs:
        print("no pairs with data in both phases")
        return 1

    positive = sum(1 for d in diffs if d > 0)
    negative = sum(1 for d in diffs if d < 0)
    print()
    print(f"pairs compared: {len(diffs)}")
    print(f"mean(B-A): {statistics.mean(diffs):.2f} ms   stdev: {statistics.pstdev(diffs):.2f} ms")
    print(f"B higher than A: {positive}/{len(diffs)}   B lower: {negative}/{len(diffs)}")
    print()
    if max(positive, negative) / len(diffs) >= 0.75:
        print("Mostly one direction: consistent with a systemic shift (e.g. background load),")
        print("not scattered noise from an alignment problem.")
    else:
        print("Mixed directions: NOT a clean one-way shift. Worth a closer look at the replay itself.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
