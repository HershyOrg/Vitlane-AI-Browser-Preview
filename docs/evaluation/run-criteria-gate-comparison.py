#!/usr/bin/env python3
"""Compare quote-only and criteria validation gates; never calls a model or a website."""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--json-jar", type=Path, required=True)
parser.add_argument("--javac", default="javac")
parser.add_argument("--java", default="java")
parser.add_argument("--output", type=Path, required=True)
args = parser.parse_args()
root = Path(__file__).resolve().parents[2]
source_dir = root / "mobile/modules/vitlane-browser/android/src/main/java/com/vitlane/browser"
sources = [source_dir / name for name in (
    "BrowserAgent.java", "BrowserTaskMemory.java", "BrowserTaskContract.java", "BrowserRecoveryPolicy.java"
)] + [Path(__file__).with_name("BrowserCriteriaGateComparison.java")]
with tempfile.TemporaryDirectory(prefix="vitlane-criteria-comparison-") as temporary:
    directory = Path(temporary)
    copied = []
    hashes = {}
    for path in sources:
        data = path.read_bytes()
        hashes[str(path.relative_to(root))] = hashlib.sha256(data).hexdigest()
        copy = directory / path.name
        copy.write_bytes(data)
        copied.append(copy)
    subprocess.run([args.javac, "-encoding", "UTF-8", "--release", "8", "-cp",
                    str(args.json_jar.resolve()), "-d", str(directory), *map(str, copied)], check=True)
    output = subprocess.check_output([args.java, "-cp", os.pathsep.join((str(directory), str(args.json_jar.resolve()))),
                                      "com.vitlane.browser.BrowserCriteriaGateComparison"], text=True)
    result = json.loads(output)
    result["runDate"] = datetime.datetime.now(datetime.timezone(datetime.timedelta(hours=9))).date().isoformat()
    result["sourceSha256"] = hashes
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"{len(result['cases'])} deterministic cases completed; results: {args.output}")
