# Long-running examples

Programs designed to expose two distinct gVisor checkpoint/restore behaviors.

## `slow_import.py` — cold-start amortization

Heavy stdlib imports + a small heap, then exit. On a fresh interpreter the
import work dominates (~150–400ms). A checkpoint taken after imports complete
can serve many user requests, so the relevant metric is `restore_ms` averaged
across runs that share that baseline.

```sh
sandbox run --language python \
  --code "$(cat examples/longrunning/slow_import.py)"
```

Once checkpointing is wired up, look for `cache_hit=true` and a `restore_ms`
span in the runner response.

## `long_compute.py` — mid-execution pause/resume

Quick startup, then a long iterative loop (stand-in for ML training,
simulation, batch processing). The interesting checkpoint is taken
*mid-loop*; restore resumes the exact iteration. This snapshot is unique to
the run — not reusable across requests — so it measures pause/resume cost,
not cold-start amortization.

```sh
sandbox run --language python \
  --code "$(cat examples/longrunning/long_compute.py)"
```

## Per-stage breakdown

```sh
sandbox trace --ip <vm-ip> --runner-token <tok> \
  --language python --code "$(cat examples/longrunning/slow_import.py)" \
  --n 20
```

The trace output now includes `checkpoint_ms`, `restore_ms`, and a cache-hit
count when checkpointing is active.
