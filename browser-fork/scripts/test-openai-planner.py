#!/usr/bin/env python3
"""Compile/test the native planner with a local JDK and org.json JAR; no API key/network."""
import argparse
import os
from pathlib import Path
import subprocess
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--json-jar", required=True, type=Path, help="Local org.json implementation JAR")
parser.add_argument("--android-jar", type=Path, help="Optionally check production APIs against Android")
parser.add_argument("--javac", default="javac")
parser.add_argument("--java", default="java")
args = parser.parse_args()
root = Path(__file__).resolve().parents[1]
source = root / "chromium/java/org/chromium/chrome/browser/lane/LaneOpenAiPlanner.java"
test = root / "tests/java/LaneOpenAiPlannerTest.java"
with tempfile.TemporaryDirectory(prefix="vitlane-planner-") as temporary:
    output = Path(temporary)
    if args.android_jar:
        android_classes = output / "android"
        android_classes.mkdir()
        subprocess.run([args.javac, "-encoding", "UTF-8", "--release", "8", "-cp",
                        str(args.android_jar.resolve()), "-d", str(android_classes), str(source)], check=True)
        print("Planner Android API compilation passed", flush=True)
    subprocess.run([args.javac, "-encoding", "UTF-8", "--release", "8", "-cp",
                    str(args.json_jar.resolve()), "-d", str(output), str(source), str(test)], check=True)
    subprocess.run([args.java, "-cp", os.pathsep.join([str(output), str(args.json_jar.resolve())]),
                    "org.chromium.chrome.browser.lane.LaneOpenAiPlannerTest",
                    str(root / "protocol/fixtures/golden-step-request.v1.json")], check=True)
