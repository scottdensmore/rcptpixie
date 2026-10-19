"""Select an installed x64 Go toolchain, never invoke an installer or shim."""
import os
import pathlib
import re
import subprocess

root = pathlib.Path(os.environ["RUNNER_TOOL_CACHE"]) / "go"
name = "go.exe" if os.name == "nt" else "go"
choices = []
for binary in root.glob("*/x64/bin/" + name):
    version = binary.parents[2].name
    if re.fullmatch(r"[0-9]+[.][0-9]+[.][0-9]+", version):
        choices.append((tuple(map(int, version.split("."))), binary))
if not choices:
    raise SystemExit("No preinstalled x64 Go toolchain; installation is not authorized")
_, binary = max(choices)
subprocess.run([str(binary), "version"], check=True)
subprocess.run([str(binary), "list", "-m"], check=True)
with open(os.environ["GITHUB_PATH"], "a", encoding="utf-8") as output:
    output.write(str(binary.parent) + "\n")
