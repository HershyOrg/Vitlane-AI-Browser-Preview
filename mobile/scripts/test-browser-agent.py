#!/usr/bin/env python3
"""Run native browser planner contracts without an Android device or OpenAI account."""
import argparse
import os
from pathlib import Path
import subprocess
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--json-jar", required=True, type=Path)
parser.add_argument("--android-jar", type=Path)
parser.add_argument("--javac", default="javac")
parser.add_argument("--java", default="java")
args = parser.parse_args()
root = Path(__file__).resolve().parents[1]
source = root / "modules/vitlane-browser/android/src/main/java/com/vitlane/browser/BrowserAgent.java"
test = root / "tests/java/BrowserAgentTest.java"

with tempfile.TemporaryDirectory(prefix="vitlane-browser-agent-") as temporary:
    output = Path(temporary)
    if args.android_jar:
        android_classes = output / "android"
        android_classes.mkdir()
        subprocess.run([args.javac, "-encoding", "UTF-8", "--release", "8", "-cp",
                        str(args.android_jar.resolve()), "-d", str(android_classes), str(source)], check=True)
        print("BrowserAgent Android API compilation passed", flush=True)
    subprocess.run([args.javac, "-encoding", "UTF-8", "--release", "8", "-cp",
                    str(args.json_jar.resolve()), "-d", str(output), str(source), str(test)], check=True)
    subprocess.run([args.java, "-cp", os.pathsep.join([str(output), str(args.json_jar.resolve())]),
                    "com.vitlane.browser.BrowserAgentTest"], check=True)
