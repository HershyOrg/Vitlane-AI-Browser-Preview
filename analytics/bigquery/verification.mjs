// Builds bounded, table-free BigQuery verification jobs from the production SQL.
// No credentials or API calls. Pipe the JSON to an authenticated verifier.
import { readFileSync } from "node:fs";
const quote = value => JSON.stringify(value);
const rows = [];
function event(day, name, user, browser, key, legacyKey, action = "") {
  const params = [["schema_version", 1], ["action", action]];
  if (key) params.push(["event_key", key]);
  if (legacyKey) params.push(["event_id", legacyKey]);
  const values = params.map(([k, v]) => "STRUCT(" + quote(k) + " AS key, STRUCT(" +
    (typeof v === "string" ? quote(v) : "CAST(NULL AS STRING)") + " AS string_value, " +
    (typeof v === "number" ? v : "CAST(NULL AS INT64)") + " AS int_value) AS value)");
  rows.push("SELECT UNIX_MICROS(TIMESTAMP " + quote(day) + ") AS event_timestamp, " +
    quote(name) + " AS event_name, " + quote(user) + " AS user_id, " +
    quote(browser) + " AS user_pseudo_id, [" + values.join(",") +
    "] AS event_params, " + quote(day.replaceAll("-", "")) + " AS _TABLE_SUFFIX");
}
event("2026-07-01", "page_view", "", "browser-a", "page-a");
event("2026-07-01", "page_view", "", "browser-a", "page-a"); // duplicate
event("2026-07-01", "page_view", "", "missing-key-browser"); // excluded
event("2026-07-01", "curation_created", "account-a", "browser-a", "new-a", "legacy-a");
event("2026-07-01", "curation_created", "account-a", "browser-a", "new-a", "different-legacy"); // prefer new key
event("2026-07-01", "curation_created", "account-b", "browser-b", null, "legacy-b"); // old server packet
event("2026-07-08", "research_results_viewed", "account-a", "browser-a", "return-a");
event("2026-07-31", "external_purchase_reported", "account-a", "browser-a", "report-a", null, "REPORTED");
event("2026-09-20", "curation_created", "young-account", "browser-c", "young-a");
function job(name, dates, expected) {
  const source = readFileSync(new URL(name + ".sql", import.meta.url), "utf8");
  const table = String.fromCharCode(96) + "YOUR_PROJECT.analytics_PROPERTY.events_*" + String.fromCharCode(96);
  const query = source.replace(table, "fixture")
    .replace(/^WITH /m, "WITH fixture AS (" + rows.join("\nUNION ALL\n") + "), ");
  return {
    name, expected,
    request: {
      query, useLegacySql: false, maximumBytesBilled: "1000000", location: "asia-northeast3",
      parameterMode: "NAMED", queryParameters: Object.entries(dates).map(([name, value]) => ({
        name, parameterType: { type: "DATE" }, parameterValue: { value },
      })),
    },
  };
}
console.log(JSON.stringify([
  job("usage", {start_date:"2026-07-01",end_date:"2026-07-02"}, [
    ["1","2","2","0"],
  ]),
  job("cohorts", {export_start:"2026-07-01",as_of:"2026-09-24"}, [
    ["2026-07-01","2","2","1","2","1"],
    ["2026-09-01","1","0","0","0","0"],
  ]),
]));
