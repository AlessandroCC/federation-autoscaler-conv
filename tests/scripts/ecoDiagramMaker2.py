#!/usr/bin/env python3
"""
ecoDiagramMaker2.py
====================

Same as ecoDiagramMaker.py (see that file for the full description), with
ONE difference: the dead time between Phase A and Phase B is removed from
the X axis before plotting.

Why: Phase B does not start the instant Phase A ends -- there is a real
wall-clock gap while the harness switches every Consumer's policy and waits
for it to propagate (policyPropagationWait / advertisementLag). Because each
Consumer's first Eco reservation lands at a slightly different instant, the
raw aggregate timeline (ecoDiagramMaker.py's output) shows a brief
transitional step right after the policy-switch line, where some Consumers
still carry their last Random-era value while others have already updated --
before settling into Eco's true steady state.

This variant shifts every Phase B timestamp backward by a single fixed
delta = (first Phase B event) - (last Phase A event), computed from the RAW
(unfiltered) data. Phase B's own internal spacing between its events is
preserved exactly -- only the whole phase is translated earlier in time, so
it appears to begin immediately where Phase A left off. Nothing about Phase A
changes. The removed delta is reported in the chart legend and in
carbon_summary.md so the compression is never silently invisible.

Generic by design: the number of Consumers is auto-detected from the CSV
(nothing is hardcoded), so the exact same command works unchanged for every
experiment size, e.g.:

    python3 tests/scripts/ecoDiagramMaker2.py --input results/3c-7p/reservations.csv
    python3 tests/scripts/ecoDiagramMaker2.py --input results/8c-17p/reservations.csv
    python3 tests/scripts/ecoDiagramMaker2.py --input results/15c-35p/reservations.csv
    python3 tests/scripts/ecoDiagramMaker2.py --input results/30c-70p/reservations.csv

Outputs (default: an `analysis/` directory next to the input file; override
with --output-dir) -- same four files as ecoDiagramMaker.py; run both
scripts against the same input into different --output-dir values to compare
the uncompressed and gap-compressed views side by side:

    aggregate_carbon_intensity.png   -- 300+ DPI step chart
    aggregate_carbon_intensity.pdf   -- vector version of the same chart
    aggregate_carbon_intensity.csv   -- the underlying timeline data
    carbon_summary.md                -- text summary + sanity-check warnings

Only `reservations.csv` is read; no other experiment output file is required.

Requires: Python 3, pandas, matplotlib (standard library otherwise).
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

import pandas as pd

import matplotlib

matplotlib.use("Agg")  # headless-safe: no display needed (e.g. on a remote server)
import matplotlib.pyplot as plt  # noqa: E402  (must follow matplotlib.use)

# Columns this script expects to find in the input CSV. Not all of them feed
# the computation directly (e.g. "action" and "provider_id" are validated for
# completeness / future use), but a comparative-eco reservations.csv always
# carries all of them (see reservationCSVHeader in tests/testlib/writer.go).
REQUIRED_COLUMNS = [
    "timestamp",
    "consumer_id",
    "phase",
    "policy",
    "provider_id",
    "action",
    "carbon_intensity",
    "outcome",
    "final_phase",
]

# The two phase labels this test harness always produces (testlib.PhaseA /
# testlib.PhaseB). Hardcoding these two known literals is what lets phase
# boundaries be "detected automatically from the phase column" without the
# caller having to tell the script which timestamp the switch happened at.
PHASE_A = "phase-a"
PHASE_B = "phase-b"


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        prog="ecoDiagramMaker2.py",
        description=(
            "Same as ecoDiagramMaker.py, but the dead time between Phase A and "
            "Phase B (policy-switch propagation wait) is removed from the X axis: "
            "every Phase B timestamp is shifted backward by a fixed delta so Phase "
            "B appears to start immediately where Phase A left off, while its own "
            "internal event spacing is preserved exactly. Works unchanged for any "
            "experiment size -- the number of Consumers is auto-detected from the "
            "CSV."
        ),
        epilog=(
            "examples (identical command, only --input changes with scale):\n"
            "  python3 tests/scripts/ecoDiagramMaker2.py --input results/3c-7p/reservations.csv\n"
            "  python3 tests/scripts/ecoDiagramMaker2.py --input results/8c-17p/reservations.csv\n"
            "  python3 tests/scripts/ecoDiagramMaker2.py --input results/15c-35p/reservations.csv\n"
            "  python3 tests/scripts/ecoDiagramMaker2.py --input results/30c-70p/reservations.csv\n"
        ),
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument(
        "--input",
        required=True,
        type=Path,
        help="Path to a comparative-eco reservations.csv file.",
    )
    parser.add_argument(
        "--output-dir",
        type=Path,
        default=None,
        help="Output directory (default: an 'analysis' folder next to --input).",
    )
    parser.add_argument(
        "--grid",
        choices=["regular", "events"],
        default="regular",
        help=(
            "How to build the aggregation timeline. 'regular' (default) resamples "
            "onto an evenly spaced grid (see --grid-minutes); 'events' uses every "
            "distinct valid event timestamp instead, giving an exact step function "
            "at the cost of a less even spacing."
        ),
    )
    parser.add_argument(
        "--grid-minutes",
        type=float,
        default=1.0,
        help="Grid spacing in minutes when --grid=regular (default: 1.0).",
    )
    parser.add_argument(
        "--transition-at-minutes",
        type=float,
        default=None,
        help=(
            "Anchor the Phase A -> Phase B switch at exactly this many elapsed "
            "minutes, instead of closing the gap only up to Phase A's last real "
            "event. Useful when phases ran for a fixed configured duration (e.g. "
            "experiment.duration: time with experiment.timer: 30m) but the last "
            "Consumer's last iteration landed a little before that deadline: "
            "without this flag the switch line sits wherever Phase A's data "
            "happens to end (e.g. 29.4 min), not at the intended 30. Default: "
            "unset, falls back to closing the real observed gap."
        ),
    )
    args = parser.parse_args(argv)
    if args.grid == "regular" and args.grid_minutes <= 0:
        parser.error("--grid-minutes must be a positive number")
    return args


def load_reservations(path: Path) -> pd.DataFrame:
    """Reads and validates the input CSV. Exits with a clear message on any
    problem, per the "produce useful errors" requirement -- this script is
    meant to be run by hand while writing a thesis, not from a pipeline."""
    if not path.is_file():
        sys.exit(f"error: input file not found: {path}")

    try:
        df = pd.read_csv(path, dtype=str)
    except Exception as exc:  # noqa: BLE001 -- surfaced to the user as-is
        sys.exit(f"error: failed to read CSV '{path}': {exc}")

    if df.empty:
        sys.exit(f"error: input CSV '{path}' has no data rows.")

    missing = [c for c in REQUIRED_COLUMNS if c not in df.columns]
    if missing:
        sys.exit(
            "error: input CSV is missing required column(s): "
            + ", ".join(missing)
            + f"\n  found columns: {', '.join(df.columns)}"
            + "\n  this script expects a comparative-eco reservations.csv "
            "(see tests/testlib/writer.go, ReservationRecord) -- a "
            "comparative-latency reservations.csv has no carbon data."
        )

    # Robust ISO-8601 parsing: Go's time.RFC3339Nano (what writes these
    # timestamps) trims trailing zero fractional digits, so different rows
    # can have different fractional-second precision -- a fixed strptime
    # format would break on that. pd.to_datetime's flexible parser handles
    # the variable precision; errors="coerce" turns anything unparseable
    # into NaT instead of raising, so a handful of bad rows do not kill the
    # whole run.
    df["timestamp"] = pd.to_datetime(df["timestamp"], utc=True, errors="coerce")
    n_bad_ts = int(df["timestamp"].isna().sum())
    if n_bad_ts:
        print(
            f"warning: {n_bad_ts} row(s) had an unparseable timestamp and were dropped",
            file=sys.stderr,
        )
        df = df.dropna(subset=["timestamp"])
    if df.empty:
        sys.exit("error: no rows with a parseable timestamp remain in the input.")

    df["carbon_intensity"] = pd.to_numeric(df["carbon_intensity"], errors="coerce")
    return df


def compress_transition_gap(
    df: pd.DataFrame,
    t0: pd.Timestamp,
    transition_at_minutes: float | None,
) -> tuple[pd.DataFrame, pd.Timedelta | None]:
    """Shifts every Phase B row's timestamp backward by a fixed delta, closing
    the dead time between Phase A and Phase B on the X axis. Phase A is left
    untouched, and every Phase B row moves by the exact same delta, so Phase
    B's own internal timing (its real cadence between iterations) is
    preserved -- only the phase as a whole is translated earlier.

    Where the switch lands depends on transition_at_minutes:

    - None (default): closes only the real observed gap, anchoring Phase B's
      first event immediately after Phase A's *last actual event*. If Phase A
      stopped producing events a little before its configured deadline (its
      per-Consumer loops each stop as soon as they notice the deadline has
      passed, so the very last recorded event is rarely exactly at the
      deadline), the switch line lands wherever that last event happened to
      be -- e.g. 29.4 min instead of a configured 30.
    - a float: anchors Phase B's first event at exactly t0 + that many
      minutes instead, regardless of when Phase A's data actually stopped.
      This is what removes BOTH gaps at once when phases ran for a fixed
      configured duration (experiment.duration: time, experiment.timer): the
      slack between Phase A's last real event and its true deadline, AND the
      policy-switch propagation wait after that deadline.

    Returns (shifted_df, delta); delta is None (df returned unchanged) when
    Phase B is missing from the input, in which case there is no gap to
    remove."""
    has_b = (df["phase"] == PHASE_B).any()
    if not has_b:
        return df, None

    first_b = df.loc[df["phase"] == PHASE_B, "timestamp"].min()
    if transition_at_minutes is not None:
        anchor = t0 + pd.Timedelta(minutes=transition_at_minutes)
    else:
        has_a = (df["phase"] == PHASE_A).any()
        if not has_a:
            return df, None
        anchor = df.loc[df["phase"] == PHASE_A, "timestamp"].max()
    delta = first_b - anchor

    out = df.copy()
    b_mask = out["phase"] == PHASE_B
    out.loc[b_mask, "timestamp"] = out.loc[b_mask, "timestamp"] - delta
    return out, delta


def filter_valid(df: pd.DataFrame) -> pd.DataFrame:
    """Rows that may update a Consumer's active carbon-intensity state:
    a successful, Peered reservation with a numeric carbon_intensity and a
    real consumer_id. Everything else (failed/incomplete records) is
    excluded here and therefore never touches the per-Consumer state --
    which is exactly what "failed reservations must not update a Consumer's
    active carbon-intensity state" requires, simply by construction."""
    consumer_id = df["consumer_id"].fillna("")
    mask = (
        (df["outcome"] == "success")
        & (df["final_phase"] == "Peered")
        & df["carbon_intensity"].notna()
        & df["consumer_id"].notna()
        & (consumer_id.str.strip() != "")
    )
    return df[mask].copy()


def phase_duration(df: pd.DataFrame, phase_value: str) -> pd.Timedelta | None:
    """Wall-clock span of a phase, from the RAW (unfiltered) data -- this is
    the true duration the harness ran that phase for, regardless of whether
    every attempt inside it succeeded. Called AFTER compress_transition_gap,
    so a Phase B duration computed here is unaffected by the shift (a
    uniform translation does not change a span)."""
    ts = df.loc[df["phase"] == phase_value, "timestamp"]
    if ts.empty:
        return None
    return ts.max() - ts.min()


def detect_phase_transition(df: pd.DataFrame) -> pd.Timestamp | None:
    """The Random-to-Eco switch instant: the first timestamp (in the RAW,
    unfiltered data) at which Phase B activity appears. Using the raw data
    rather than only valid rows means the boundary is correct even if the
    very first Phase B attempt happened to fail. Called AFTER
    compress_transition_gap, so this now coincides with the last Phase A
    event -- the whole point of the shift."""
    phase_b_ts = df.loc[df["phase"] == PHASE_B, "timestamp"]
    if phase_b_ts.empty:
        return None
    return phase_b_ts.min()


def phase_policy_name(df: pd.DataFrame, phase_value: str, default: str) -> str:
    """The policy name actually recorded for a phase (e.g. "Random", "Eco"),
    read from the data instead of hardcoded, so the chart/labels stay
    correct even if policy names ever change."""
    values = df.loc[df["phase"] == phase_value, "policy"].dropna()
    values = values[values.str.strip() != ""]
    return values.iloc[0] if not values.empty else default


def build_state_matrix(valid: pd.DataFrame) -> pd.DataFrame:
    """Wide matrix: index = distinct valid-event timestamp (sorted), one
    column per Consumer, values = that Consumer's active carbon intensity
    as of that timestamp. Forward-filled per column, so a cell holds the
    last successful Peered reading until the next one for that Consumer --
    and stays NaN (missing, not zero) before that Consumer's first success,
    per "if no successful reservation exists yet for a Consumer, keep it
    missing"."""
    v = valid.sort_values("timestamp")
    # Guard pivot() against two valid events for the same Consumer landing on
    # the exact same timestamp (should not happen in practice, but pivot()
    # raises on duplicate (index, column) pairs) -- keep the later one.
    v = v.drop_duplicates(subset=["timestamp", "consumer_id"], keep="last")
    wide = v.pivot(index="timestamp", columns="consumer_id", values="carbon_intensity")
    return wide.sort_index().ffill()


def build_timeline_index(wide: pd.DataFrame, grid: str, grid_minutes: float) -> pd.DatetimeIndex:
    if grid == "events":
        return wide.index
    t0, t1 = wide.index.min(), wide.index.max()
    idx = pd.date_range(t0, t1, freq=pd.Timedelta(minutes=grid_minutes))
    if idx.empty or idx[-1] < t1:
        idx = idx.append(pd.DatetimeIndex([t1]))
    return idx


def resample_state(wide: pd.DataFrame, target_index: pd.DatetimeIndex) -> pd.DataFrame:
    """Evaluates the per-Consumer state matrix at target_index. A plain
    .reindex(target_index) would only align on exact timestamp matches and
    leave everything else NaN; unioning the real event index in first, then
    forward-filling, then dropping back to target_index correctly carries
    each Consumer's last known value onto every requested point (regular
    grid or otherwise) while still leaving genuinely-not-yet-active
    Consumers as NaN."""
    unioned = wide.reindex(wide.index.union(target_index)).sort_index().ffill()
    return unioned.reindex(target_index)


def compute_aggregate(state: pd.DataFrame) -> pd.DataFrame:
    active_consumers = state.notna().sum(axis=1)  # int64, kept as a clean count
    aggregate = state.sum(axis=1, skipna=True)
    # active_consumers is 0 exactly when no Consumer has succeeded yet (before
    # anyone's first event). Divide using a float64 copy with those zeros
    # replaced by plain NaN (not pd.NA, which can upcast an int Series to
    # object dtype and behave inconsistently across pandas versions), so
    # average_carbon_intensity is correctly left blank instead of becoming
    # inf or raising a divide-by-zero.
    denom = active_consumers.astype("float64").replace(0.0, float("nan"))
    average = aggregate / denom
    return pd.DataFrame(
        {
            "timestamp": state.index,
            "active_consumers": active_consumers.to_numpy(),
            "aggregate_carbon_intensity": aggregate.to_numpy(),
            "average_carbon_intensity": average.to_numpy(),
        }
    )


def annotate_timeline(
    timeline: pd.DataFrame,
    transition_ts: pd.Timestamp | None,
    t0: pd.Timestamp,
    phase_a_label: str,
    phase_b_label: str,
) -> pd.DataFrame:
    out = timeline.copy()
    out["elapsed_minutes"] = (out["timestamp"] - t0).dt.total_seconds() / 60.0
    if transition_ts is not None:
        out["phase"] = [PHASE_A if t < transition_ts else PHASE_B for t in out["timestamp"]]
    else:
        out["phase"] = PHASE_A
    out["policy"] = out["phase"].map({PHASE_A: phase_a_label, PHASE_B: phase_b_label})
    return out[
        [
            "timestamp",
            "elapsed_minutes",
            "phase",
            "policy",
            "active_consumers",
            "aggregate_carbon_intensity",
            "average_carbon_intensity",
        ]
    ]


def make_chart(
    timeline: pd.DataFrame,
    transition_min: float | None,
    gap_removed: pd.Timedelta | None,
    subtitle: str,
    phase_a_label: str,
    phase_b_label: str,
    png_out: Path,
    pdf_out: Path,
) -> None:
    plt.rcParams.update(
        {
            "font.size": 11,
            "axes.grid": True,
            "grid.alpha": 0.35,
            "grid.linestyle": "--",
            "grid.linewidth": 0.6,
            "axes.spines.top": False,
            "axes.spines.right": False,
            "figure.facecolor": "white",
            "axes.facecolor": "white",
            "savefig.facecolor": "white",
        }
    )
    fig, ax = plt.subplots(figsize=(10, 5.5))

    a = timeline[timeline["phase"] == PHASE_A]
    b = timeline[timeline["phase"] == PHASE_B]
    xmax = float(timeline["elapsed_minutes"].max())

    if transition_min is not None:
        ax.axvspan(0, transition_min, color="red", alpha=0.06, zorder=0)
        ax.axvspan(transition_min, max(xmax, transition_min), color="green", alpha=0.06, zorder=0)
        switch_label = f"Policy switch (t = {transition_min:.1f} min)"
        if gap_removed is not None:
            switch_label += f"\n[{gap_removed.total_seconds():.0f}s transition gap removed]"
        ax.axvline(
            transition_min,
            color="#333333",
            linestyle="--",
            linewidth=1.2,
            label=switch_label,
        )

    if not a.empty:
        seg_a = a
        # Include Phase B's first point in the red segment too, purely for
        # plotting, so the red step visually reaches the transition line
        # instead of stopping one step short of it (the two colors then meet
        # exactly at the switch instead of leaving a gap).
        if transition_min is not None and not b.empty:
            seg_a = pd.concat([a, b.iloc[[0]]], ignore_index=True)
        ax.step(
            seg_a["elapsed_minutes"],
            seg_a["aggregate_carbon_intensity"],
            where="post",
            color="#c0392b",
            linewidth=1.8,
            label=f"Phase A ({phase_a_label})",
        )

    if not b.empty:
        ax.step(
            b["elapsed_minutes"],
            b["aggregate_carbon_intensity"],
            where="post",
            color="#1e8449",
            linewidth=1.8,
            label=f"Phase B ({phase_b_label})",
        )

    ax.set_xlabel("Elapsed time [minutes] (transition gap removed)")
    ax.set_ylabel("Sum of selected-provider carbon intensities [gCO2eq/kWh]")
    ax.set_xlim(left=0)
    ax.set_ylim(bottom=0)

    fig.suptitle("Aggregate Carbon-Intensity Indicator Over Time", fontsize=13, fontweight="bold", y=0.98)
    ax.set_title(subtitle, fontsize=9.5, color="#555555", pad=10)

    ax.legend(loc="best", frameon=True, framealpha=0.9)
    fig.tight_layout(rect=(0, 0, 1, 0.94))

    fig.savefig(png_out, dpi=300)
    fig.savefig(pdf_out)
    plt.close(fig)


def write_summary(
    path: Path,
    *,
    input_path: Path,
    n_detected: int,
    valid_consumer_ids: list[str],
    missing_consumers: list[str],
    t_first: pd.Timestamp,
    t_last: pd.Timestamp,
    phase_a_label: str,
    phase_b_label: str,
    dur_a: pd.Timedelta | None,
    dur_b: pd.Timedelta | None,
    n_valid: int,
    n_ignored: int,
    timeline: pd.DataFrame,
    grid_mode: str,
    grid_minutes: float,
    gap_removed: pd.Timedelta | None,
) -> None:
    def fmt_td(td: pd.Timedelta | None) -> str:
        if td is None:
            return "n/a (phase not present in the input)"
        total = td.total_seconds()
        return f"{total / 60:.2f} min ({total:.1f} s)"

    def fmt_val(v: float) -> str:
        return f"{v:.2f} gCO2eq/kWh" if pd.notna(v) else "n/a"

    def diff_block(a: float, b: float, label: str) -> str:
        if pd.isna(a) or pd.isna(b):
            return f"- {label}: n/a (insufficient data in one phase)\n"
        abs_diff = b - a
        pct_diff = (abs_diff / a * 100.0) if a != 0 else float("nan")
        direction = "decrease" if abs_diff < 0 else "increase"
        pct_str = f"{pct_diff:+.1f}%" if pd.notna(pct_diff) else "n/a"
        return f"- {label}: {abs_diff:+.2f} gCO2eq/kWh ({pct_str} {direction} from Phase A to Phase B)\n"

    mean_agg_a = timeline.loc[timeline["phase"] == PHASE_A, "aggregate_carbon_intensity"].mean()
    mean_agg_b = timeline.loc[timeline["phase"] == PHASE_B, "aggregate_carbon_intensity"].mean()
    mean_avg_a = timeline.loc[timeline["phase"] == PHASE_A, "average_carbon_intensity"].mean()
    mean_avg_b = timeline.loc[timeline["phase"] == PHASE_B, "average_carbon_intensity"].mean()

    warnings_lines: list[str] = []
    min_active = int(timeline["active_consumers"].min())
    max_active = int(timeline["active_consumers"].max())
    if min_active != max_active:
        warnings_lines.append(
            f"- Active Consumer count varied over the run: between {min_active} and "
            f"{max_active} (of {n_detected} detected in the file).\n"
        )
    if missing_consumers:
        warnings_lines.append(
            f"- {len(missing_consumers)} Consumer(s) never had a successful Peered "
            f"reservation and contributed no data: {', '.join(missing_consumers)}.\n"
        )
    if not warnings_lines:
        warnings_lines.append("- None.\n")

    grid_desc = f"regular ({grid_minutes:g} min spacing)" if grid_mode == "regular" else "events (exact timestamps)"
    gap_desc = (
        f"{gap_removed.total_seconds():.1f} s removed from the X axis "
        "(Phase B timestamps shifted backward by this fixed amount; Phase B's own "
        "internal event spacing is unchanged)"
        if gap_removed is not None
        else "n/a (Phase A or Phase B missing from the input)"
    )

    lines = [
        "# Carbon Intensity Analysis Summary (transition-gap-compressed)\n\n",
        "**This is the gap-compressed variant** -- see `ecoDiagramMaker.py` for the "
        "uncompressed view of the same run.\n\n",
        f"Input: `{input_path}`\n\n",
        f"- Detected Consumers: {n_detected}\n",
        f"- Consumers with at least one valid reservation: {len(valid_consumer_ids)}\n",
        f"- First valid event: {t_first.isoformat()}\n",
        f"- Last valid event: {t_last.isoformat()}\n",
        f"- Phase A -> Phase B transition gap: {gap_desc}\n",
        f"- Phase A ({phase_a_label}) duration: {fmt_td(dur_a)}\n",
        f"- Phase B ({phase_b_label}) duration: {fmt_td(dur_b)}\n",
        f"- Valid successful Peered reservation records: {n_valid}\n",
        f"- Ignored failed/incomplete records: {n_ignored}\n",
        f"- Timeline grid: {grid_desc}\n",
        "\n## Aggregate carbon-intensity indicator\n\n",
        f"- Mean aggregate (Phase A, {phase_a_label}): {fmt_val(mean_agg_a)}\n",
        f"- Mean aggregate (Phase B, {phase_b_label}): {fmt_val(mean_agg_b)}\n",
        diff_block(mean_agg_a, mean_agg_b, "Aggregate difference (A -> B)"),
        "\n## Mean per-active-Consumer carbon intensity\n\n",
        f"- Phase A ({phase_a_label}): {fmt_val(mean_avg_a)}\n",
        f"- Phase B ({phase_b_label}): {fmt_val(mean_avg_b)}\n",
        diff_block(mean_avg_a, mean_avg_b, "Per-Consumer difference (A -> B)"),
        "\n## Warnings\n\n",
        *warnings_lines,
        "\n## Reproduce\n\n",
        "```bash\n",
        f"python3 tests/scripts/ecoDiagramMaker2.py --input {input_path}\n",
        "```\n",
    ]
    path.write_text("".join(lines), encoding="utf-8")


def main(argv: list[str] | None = None) -> None:
    args = parse_args(argv)
    input_path = args.input
    output_dir = args.output_dir or (input_path.parent / "analysis")
    output_dir.mkdir(parents=True, exist_ok=True)

    df = load_reservations(input_path)

    # t0 (elapsed_minutes' zero point) is always Phase A's first valid event
    # -- Phase A always precedes Phase B in wall-clock time -- so it can (and
    # must) be computed BEFORE compress_transition_gap runs: the shift only
    # ever moves Phase B rows, and --transition-at-minutes needs t0 to know
    # where "t0 + N minutes" actually falls.
    pre_shift_valid = filter_valid(df)
    if pre_shift_valid.empty:
        sys.exit(
            "error: no valid rows found (need outcome=='success', "
            "final_phase=='Peered', and a numeric carbon_intensity). "
            "Is this a comparative-eco reservations.csv?"
        )
    t0 = pre_shift_valid["timestamp"].min()

    df, gap_removed = compress_transition_gap(df, t0, args.transition_at_minutes)
    valid = filter_valid(df)  # re-filter: same rows, Phase B timestamps now shifted
    n_ignored = len(df) - len(valid)

    all_consumer_ids = sorted(df["consumer_id"].dropna().unique())
    valid_consumer_ids = sorted(valid["consumer_id"].unique())
    n_detected = len(all_consumer_ids)
    missing_consumers = sorted(set(all_consumer_ids) - set(valid_consumer_ids))

    transition_ts = detect_phase_transition(df)
    t_last = valid["timestamp"].max()

    phase_a_label = phase_policy_name(df, PHASE_A, "Phase A")
    phase_b_label = phase_policy_name(df, PHASE_B, "Phase B")

    wide = build_state_matrix(valid)
    target_index = build_timeline_index(wide, args.grid, args.grid_minutes)
    state = resample_state(wide, target_index)
    timeline = compute_aggregate(state)
    timeline = annotate_timeline(timeline, transition_ts, t0, phase_a_label, phase_b_label)

    transition_min = None
    if transition_ts is not None:
        transition_min = (transition_ts - t0).total_seconds() / 60.0

    min_active = int(timeline["active_consumers"].min())
    max_active = int(timeline["active_consumers"].max())
    if min_active == max_active:
        subtitle = f"Detected Consumers: {min_active} (transition gap removed)"
    else:
        subtitle = f"Active Consumers: {min_active}–{max_active} (of {n_detected} detected) — transition gap removed"

    png_out = output_dir / "aggregate_carbon_intensity.png"
    pdf_out = output_dir / "aggregate_carbon_intensity.pdf"
    csv_out = output_dir / "aggregate_carbon_intensity.csv"
    summary_out = output_dir / "carbon_summary.md"

    make_chart(timeline, transition_min, gap_removed, subtitle, phase_a_label, phase_b_label, png_out, pdf_out)

    timeline_out = timeline.copy()
    timeline_out["timestamp"] = timeline_out["timestamp"].dt.strftime("%Y-%m-%dT%H:%M:%S.%f%z")
    timeline_out.to_csv(csv_out, index=False)

    write_summary(
        summary_out,
        input_path=input_path,
        n_detected=n_detected,
        valid_consumer_ids=valid_consumer_ids,
        missing_consumers=missing_consumers,
        t_first=t0,
        t_last=t_last,
        phase_a_label=phase_a_label,
        phase_b_label=phase_b_label,
        dur_a=phase_duration(df, PHASE_A),
        dur_b=phase_duration(df, PHASE_B),
        n_valid=len(valid),
        n_ignored=n_ignored,
        timeline=timeline,
        grid_mode=args.grid,
        grid_minutes=args.grid_minutes,
        gap_removed=gap_removed,
    )

    print("Wrote:")
    for p in (png_out, pdf_out, csv_out, summary_out):
        print(f"  {p}")
    if gap_removed is not None:
        print(f"(removed a {gap_removed.total_seconds():.1f}s transition gap between Phase A and Phase B)")


if __name__ == "__main__":
    main()
