"""Summarize actual default-suite outcomes and retain real-rasterizer coverage."""
import collections
import json
import sys

counts = collections.Counter()
skips = []
real_rasterizer_passed = False
with open(sys.argv[1], encoding="utf-8") as events:
    for line in events:
        event = json.loads(line)
        action = event.get("Action")
        if event.get("Test") and action in ("pass", "fail", "skip"):
            counts[action] += 1
            if action == "skip":
                skips.append(event["Package"] + "/" + event["Test"])
            if event["Test"] == "TestLoadScannedWithRealRasterizer" and action == "pass":
                real_rasterizer_passed = True
print("Actual test/subtest outcomes:", dict(counts))
print("Existing guarded skips:\n" + "\n".join(skips))
print("Real pdftoppm rasterizer test passed:", real_rasterizer_passed)
if sys.argv[2] == "Linux" and not real_rasterizer_passed:
    raise SystemExit("Blocked: retained Linux real-rasterizer coverage needs preinstalled pdftoppm; no installation was attempted")
