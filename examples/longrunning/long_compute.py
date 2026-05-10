"""Long-runtime program — quick startup, slow steady-state work.

Use case: measuring checkpoint/restore as a *pause/resume* mechanism for one
in-flight job (preemption, migration, spot-instance recovery). The checkpoint
is taken *mid-computation*, and restore resumes from that exact point.

This is NOT a cold-start optimization — the snapshot is unique to this run
and can't be reused across requests. It demonstrates that gVisor can
serialize live execution state, not just init state.

The loop here is a stand-in for an ML training step or any iterative compute.
Each "step" sleeps + does a small amount of CPU work so the program runs
long enough to interrupt with a checkpoint.

Test the cold path:
    sandbox run --language python --code "$(cat examples/longrunning/long_compute.py)"
"""

import time
import math

STEPS = 200
checkpoint_total = 0.0

t0 = time.perf_counter()
for step in range(STEPS):
    # Pretend "training step" — a bit of CPU + a small sleep.
    acc = 0.0
    for i in range(20_000):
        acc += math.sin(i) * math.cos(i)
    checkpoint_total += acc
    time.sleep(0.05)

    if step % 20 == 0:
        elapsed = time.perf_counter() - t0
        print(f"step={step} elapsed_ms={int(elapsed * 1000)} acc={checkpoint_total:.4f}")

elapsed = time.perf_counter() - t0
print(f"done steps={STEPS} total_ms={int(elapsed * 1000)}")
print("ok")
