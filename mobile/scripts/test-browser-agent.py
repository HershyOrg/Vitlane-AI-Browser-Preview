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
sources = [root / "modules/vitlane-browser/android/src/main/java/com/vitlane/browser" / name
           for name in ("BrowserAgent.java", "BrowserChat.java", "BrowserTaskMemory.java", "BrowserTaskContract.java", "BrowserRecoveryPolicy.java",
                        "BrowserResearchPlanner.java", "BrowserExecutionVerifier.java", "BrowserObservationPolicy.java", "BrowserLocationContext.java")]
tests = [root / "tests/java" / name for name in ("BrowserAgentTest.java", "BrowserChatTest.java", "BrowserTaskMemoryTest.java", "BrowserTaskContractTest.java",
                                                "BrowserResearchPlannerTest.java")]

with tempfile.TemporaryDirectory(prefix="vitlane-browser-agent-") as temporary:
    output = Path(temporary)
    if args.android_jar:
        android_classes = output / "android"
        android_classes.mkdir()
        subprocess.run([args.javac, "-encoding", "UTF-8", "--release", "8", "-cp",
                        str(args.android_jar.resolve()), "-d", str(android_classes), *map(str, sources)], check=True)
        print("BrowserAgent Android API compilation passed", flush=True)
    compile_classpath = [str(args.json_jar.resolve())]
    if args.android_jar: compile_classpath.append(str(args.android_jar.resolve()))
    subprocess.run([args.javac, "-encoding", "UTF-8", "--release", "8", "-cp",
                    os.pathsep.join(compile_classpath), "-d", str(output), *map(str, sources), *map(str, tests)], check=True)
    for test in tests:
        subprocess.run([args.java, "-cp", os.pathsep.join([str(output), str(args.json_jar.resolve())]),
                        "com.vitlane.browser." + test.stem], check=True)
