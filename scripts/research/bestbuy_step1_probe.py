#!/usr/bin/env python3
"""Step 1 inspection tool, not a production adapter.

Offline fixtures are synthetic. Live responses remain in memory; reports contain
counts and safe reason codes, never API keys, URLs, or provider product content.
Run from the repository root. Live mode reads BESTBUY_API_KEY from the process.
"""

import argparse
from collections import Counter
from datetime import datetime, timezone
from decimal import Decimal, InvalidOperation
import json
import os
import re
import time
import urllib.error
import urllib.parse
import urllib.request


API_ROOT = "https://api.bestbuy.com/v1/products"
SHOW = "sku,name,salePrice,priceRestriction,onlineAvailability,productVariations.sku,url"
MAX_BYTES = 1024 * 1024
MAX_RELATED = 20


class ContractError(ValueError):
    pass


def sku_ref(value):
    if isinstance(value, bool) or not isinstance(value, (str, int)):
        raise ContractError("SKU_INVALID")
    value = str(value)
    if not re.fullmatch(r"[1-9][0-9]{0,17}", value):
        raise ContractError("SKU_INVALID")
    return value


def variation_refs(row):
    value = row.get("productVariations")
    if value is None:
        return "UNKNOWN", []
    if not isinstance(value, list):
        raise ContractError("VARIATION_SHAPE_INVALID")
    own = sku_ref(row.get("sku"))
    refs = []
    for item in value:
        if not isinstance(item, dict):
            raise ContractError("VARIATION_ROW_INVALID")
        ref = sku_ref(item.get("sku"))
        if ref != own and ref not in refs:
            refs.append(ref)
    return ("RELATED_REFS" if refs else "EMPTY_OBSERVED"), refs


def observed_price(row):
    restriction = row.get("priceRestriction")
    if restriction is not None and str(restriction).strip():
        return {"kind": "UNKNOWN", "reason": "PRICE_RESTRICTED"}
    value = row.get("salePrice")
    if value is None:
        return {"kind": "UNKNOWN", "reason": "PRICE_MISSING"}
    if isinstance(value, (bool, float)):
        raise ContractError("PRICE_TYPE_INVALID")
    try:
        amount = Decimal(str(value))
        if not amount.is_finite() or amount < 0 or amount > Decimal("1000000000000"):
            raise ContractError("PRICE_RANGE_INVALID")
        minor = amount * 100
        if minor != minor.to_integral_value():
            raise ContractError("PRICE_PRECISION_INVALID")
    except InvalidOperation as error:
        raise ContractError("PRICE_INVALID") from error
    return {"kind": "OBSERVED", "amountMinor": int(minor), "currency": "USD",
            "currencyBasis": "US_PRODUCTS_API_PROFILE_TO_VERIFY_LIVE"}


def valid_product_link(value):
    if not isinstance(value, str):
        return False
    try:
        url = urllib.parse.urlsplit(value)
        if (url.scheme != "https" or url.username or url.password or
                url.port not in (None, 443)):
            return False
        if url.hostname == "api.bestbuy.com":
            return bool(re.fullmatch(r"/click/[^/]+/[0-9]+/pdp", url.path))
        return url.hostname in ("www.bestbuy.com", "bestbuy.com") and url.path.startswith("/site/")
    except ValueError:
        return False


def normalize(row):
    if not isinstance(row, dict):
        raise ContractError("PRODUCT_SHAPE_INVALID")
    sku = sku_ref(row.get("sku"))
    if not isinstance(row.get("name"), str) or not row["name"].strip():
        raise ContractError("PRODUCT_NAME_MISSING")
    relation_status, related = variation_refs(row)
    available = row.get("onlineAvailability")
    if available is not None and not isinstance(available, bool):
        raise ContractError("AVAILABILITY_TYPE_INVALID")
    return {
        "sourceProductRef": {"source": "BESTBUY", "sku": sku},
        "variantRef": {"source": "BESTBUY", "sku": sku},
        "relationStatus": relation_status,
        "relatedSkuRefs": related,
        "price": observed_price(row),
        "availability": "UNKNOWN" if available is None else "AVAILABLE" if available else "UNAVAILABLE",
        "productLinkValid": valid_product_link(row.get("url")),
        # Best Buy offers[] is promotional content, not our seller/Offer list.
        "purchaseRoute": "EXTERNAL",
        "merchantStatus": "UNVERIFIED",
    }


def parse_collection(payload, requested=None):
    if not isinstance(payload, dict) or not isinstance(payload.get("products"), list):
        raise ContractError("COLLECTION_SHAPE_INVALID")
    if len(payload["products"]) > 50:
        raise ContractError("COLLECTION_LIMIT_EXCEEDED")
    rows, seen = [], set()
    for raw in payload["products"]:
        row = normalize(raw)
        sku = row["variantRef"]["sku"]
        if requested is not None and sku not in requested:
            raise ContractError("UNREQUESTED_SKU")
        if sku in seen:
            raise ContractError("DUPLICATE_SKU")
        seen.add(sku)
        rows.append(row)
    partial = payload.get("partial", False)
    if not isinstance(partial, bool):
        raise ContractError("PARTIAL_TYPE_INVALID")
    return rows, partial


def keyword_filter(query):
    words = query.split()
    if not 1 <= len(words) <= 6 or any(not re.fullmatch(r"[A-Za-z0-9]+", w) for w in words):
        raise ContractError("QUERY_INVALID")
    return "&".join("search=" + word for word in words)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def request_products(expression, api_key):
    params = {"format": "json", "show": SHOW, "pageSize": 50}
    if api_key:
        params["apiKey"] = api_key
    url = API_ROOT + "(" + urllib.parse.quote(expression, safe="=&,()") + ")?" + urllib.parse.urlencode(params)
    request = urllib.request.Request(url, headers={"Accept": "application/json"})
    try:
        with urllib.request.build_opener(NoRedirect()).open(request, timeout=15) as response:
            raw = response.read(MAX_BYTES + 1)
            if len(raw) > MAX_BYTES:
                return None, {"reason": "RESPONSE_TOO_LARGE"}
            return json.loads(raw, parse_float=Decimal), {"httpStatus": response.status}
    except urllib.error.HTTPError as error:
        reason = "AUTH_OR_QUOTA_REJECTED" if error.code == 403 else "HTTP_ERROR"
        return None, {"httpStatus": error.code, "reason": reason}
    except (urllib.error.URLError, TimeoutError):
        return None, {"reason": "NETWORK_OR_TIMEOUT"}
    except (json.JSONDecodeError, UnicodeError):
        return None, {"reason": "INVALID_JSON"}


def audit_live(query, api_key):
    report = {"basis": "LIVE_AUTHENTICATED" if api_key else "LIVE_WITHOUT_CREDENTIALS",
              "query": query, "observedAt": datetime.now(timezone.utc).isoformat(),
              "productContentPersisted": False, "calls": []}
    payload, call = request_products(keyword_filter(query), api_key)
    report["calls"].append(call)
    if payload is None:
        report["status"] = "BLOCKED"
        return report, 2
    try:
        rows, partial = parse_collection(payload)
        report.update(searchRows=len(rows), partial=partial,
                      relationshipStates=dict(Counter(r["relationStatus"] for r in rows)),
                      priceStates=dict(Counter(r["price"]["kind"] for r in rows)),
                      validProductLinks=sum(r["productLinkValid"] for r in rows))
        related = list(dict.fromkeys(ref for r in rows for ref in r["relatedSkuRefs"]))
        requested = list(dict.fromkeys([r["variantRef"]["sku"] for r in rows[:5]] + related[:MAX_RELATED]))
        report["relatedRefsObserved"] = len(related)
        report["relatedRefsTruncated"] = len(related) > MAX_RELATED
        if requested:
            time.sleep(1)  # Audit rate: sequential and below the documented 5 requests/second.
            hydrated, call = request_products("sku in(" + ",".join(requested) + ")", api_key)
            report["calls"].append(call)
            if hydrated is None:
                report["status"] = "LOOKUP_BLOCKED"
                return report, 2
            resolved, lookup_partial = parse_collection(hydrated, set(requested))
            report.update(lookupRequested=len(requested), lookupResolved=len(resolved),
                          lookupMissing=len(requested)-len(resolved), lookupPartial=lookup_partial)
        report["status"] = "PARSED" if rows else "EMPTY"
        report["limits"] = ["No provider link navigation, product grouping, application integration, or retention permission proven."]
        return report, 0
    except ContractError as error:
        report.update(status="CONTRACT_REJECTED", reason=str(error))
        return report, 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--query", default="headset")
    parser.add_argument("--anonymous", action="store_true", help="Explicit no-key access probe only")
    args = parser.parse_args()
    try:
        keyword_filter(args.query)
    except ContractError as error:
        print(json.dumps({"status": "INVALID_INPUT", "reason": str(error)}))
        return 1
    api_key = "" if args.anonymous else os.environ.get("BESTBUY_API_KEY", "").strip()
    if not args.anonymous and not api_key:
        print(json.dumps({"status": "BLOCKED_MISSING_KEY", "liveSearchExecuted": False}))
        return 2
    report, status = audit_live(args.query, api_key)
    print(json.dumps(report, indent=2))
    return status


if __name__ == "__main__":
    raise SystemExit(main())
