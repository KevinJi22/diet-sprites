"""Long-cold-start, fast-steady-state Python program.

Use case: measuring whether gVisor checkpoint/restore can amortize *startup
work* (interpreter boot, bytecode cache miss, heavy imports) across many
user requests. The checkpoint is taken AFTER the imports complete; restore
skips them entirely.

This script is intentionally import-heavy so the cold path takes a noticeable
fraction of a second, making the checkpoint payoff easy to measure.

Test the cold path:
    sandbox run --language python --code "$(cat examples/longrunning/slow_import.py)"

Once checkpointing is wired up, the same code should restore in <200ms.
"""

import time

t0 = time.perf_counter()

# Heavy stdlib imports — each one is dozens of ms cold.
import json
import logging
import xml.etree.ElementTree as ET
import urllib.request
import http.client
import email.parser
import unittest

# Build a small in-memory data structure so the heap isn't trivial — gives the
# checkpoint image something non-zero to serialize.
records = [{"i": i, "label": f"row-{i}", "tags": ["a", "b", "c"]} for i in range(2_000)]
serialized = json.dumps(records)

t1 = time.perf_counter()
print(f"startup_ms={int((t1 - t0) * 1000)}")
print(f"records={len(records)} bytes={len(serialized)}")
print("ok")
