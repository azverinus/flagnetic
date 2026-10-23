"""The speed guarantee: evaluating a flag stays under a millisecond at p99."""

from __future__ import annotations

import os
import sys
import time
from collections import Counter
from pathlib import Path

from flagnetic._eval import decode, evaluate

from large_ruleset import LARGE_FLAGS, large_context, large_ruleset


def test_evaluation_stays_under_a_millisecond_at_p99() -> None:
    ruleset = decode(large_ruleset())
    contexts = [large_context(i) for i in range(1000)]
    calls = 50_000
    took = []
    reasons: Counter[str] = Counter()
    for i in range(calls):
        # The flag and the context advance at unrelated strides, so every flag
        # meets every kind of context.
        context = contexts[(i * 31 + i // LARGE_FLAGS) % len(contexts)]
        key = f"flag_{(i * 7919) % LARGE_FLAGS}"
        start = time.perf_counter_ns()
        result = evaluate(ruleset, context, key)
        took.append(time.perf_counter_ns() - start)
        reasons[result.reason] += 1
    took.sort()
    p50, p99 = took[calls // 2], took[calls * 99 // 100]
    line = (
        f"Python SDK evaluation on a {LARGE_FLAGS}-flag ruleset: "
        f"p50 {p50 / 1000:.1f} µs, p99 {p99 / 1000:.1f} µs (limit 1000 µs)\n"
    )
    sys.stdout.write(line)
    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary:
        with Path(summary).open("a", encoding="utf-8") as out:
            out.write(line)
    assert p99 < 1_000_000, f"p99 = {p99 / 1000:.1f} µs, want under 1 ms"
    # The ruleset must make evaluations walk its paths, or the number measures
    # less than it claims.
    for reason in ("experiment", "rollout", "rule", "prerequisite_failed", "fallthrough"):
        assert reasons[reason] > 0, f"no evaluation ended with reason {reason}: {dict(reasons)}"
