#!/usr/bin/env python3
"""Local Step 5 server: real Managed AI, Shopify fixture, Amazon saved fixture.

Use a dedicated database. This launcher never resets a database and never
prints credentials. Build web with VITE_ALLOW_DEV_AUTH_UI=true first.
"""
import argparse
import os
from pathlib import Path
import shlex
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument("--env-file", required=True)
parser.add_argument("--amazon-fixture", required=True)
parser.add_argument("--database-port", default="55448")
parser.add_argument("--database-name", default="vitlane_curation_step5")
parser.add_argument("--port", default="18087")
parser.add_argument("--stub-ai", action="store_true", help="Use the deterministic model fixture for repeatable tests")
args = parser.parse_args()
root = Path(__file__).resolve().parent.parent
for line in Path(args.env_file).read_text().splitlines():
    key, separator, raw = line.removeprefix("export ").partition("=")
    if separator and key.strip() == "MANAGED_OPENAI_API_SECRET":
        parts = shlex.split(raw, comments=True)
        if parts:
            os.environ[key.strip()] = parts[0]
if not args.stub_ai and not os.environ.get("MANAGED_OPENAI_API_SECRET"):
    raise SystemExit("Existing Managed OpenAI credential is unavailable")
fixture = Path(args.amazon_fixture).resolve()
if not fixture.is_file() or not (root / "web/dist/index.html").is_file():
    raise SystemExit("Build the web app and provide an Amazon fixture first")
os.environ.update(
    APP_ENV="development", ALLOW_DEV_AUTH="true", MIGRATE_ON_START="true",
    MIGRATIONS_DIR=str(root / "server/migrations"), WEB_DIR=str(root / "web/dist"),
    HTTP_ADDR=f"127.0.0.1:{args.port}", PUBLIC_BASE_URL=f"http://127.0.0.1:{args.port}",
    TRUSTED_BROWSER_ORIGIN=f"http://127.0.0.1:{args.port}",
    DATABASE_URL=f"postgres://vitlane:vitlane@127.0.0.1:{args.database_port}/{args.database_name}?sslmode=disable",
    DEV_AUTH_DEFAULT_USER_ID="e5000000-0000-4000-8000-000000000101",
    RESEARCH_CATALOG_PROVIDER="stub", CURATION_CATALOG_RESEARCH_ENABLED="true",
    SHOPIFY_UCP_ENABLED="false", KOREAN_PRODUCT_SEARCH_ENABLED="false",
    AMAZON_PRODUCT_SEARCH_ENABLED="true", AMAZON_PRODUCT_SEARCH_MODE="stub",
    AMAZON_PRODUCT_SEARCH_STUB_FILE=str(fixture),
    MANAGED_RUNNER_ENABLED="true", MANAGED_RUNNER_MODEL_PROVIDER="stub" if args.stub_ai else "openai",
    MANAGED_RUNNER_BASE_URL="https://api.openai.com/v1",
    CURATION_CATALOG_RESEARCH_CALLS_PER_MINUTE="60",
    CURATION_CATALOG_RESEARCH_MAX_CONCURRENCY="16", PHASE5_SETTLEMENT_ENABLED="false",
    GOCACHE="/tmp/vitlane-step5-go-cache",
)
os.environ.pop("AMAZON_PRODUCT_SEARCH_API", None)
revision = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
if subprocess.run(["git", "diff", "--quiet"], cwd=root).returncode:
    revision += "-dirty"
binary = f"/tmp/vitlane-curation-step5-{args.port}"
subprocess.run(["go", "build", "-buildvcs=false", "-ldflags", f"-X main.sourceRevision={revision}", "-o", binary, "./cmd/vitlane"], cwd=root / "server", check=True)
print(f"Step 5 review: http://127.0.0.1:{args.port} — AI {'stub' if args.stub_ai else 'live'}; Shopify/Amazon stub", flush=True)
os.execv(binary, [binary])
