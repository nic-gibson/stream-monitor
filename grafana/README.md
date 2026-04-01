# Grafana dashboards (strmon)

Import JSON from **Dashboards → New → Import → Upload JSON file** (Grafana 9+).

**Prerequisites:** Prometheus scrapes strmon (`/metrics`), e.g. job `redis-stream-mon` in `prometheus.yml`.

| File | Purpose |
|------|---------|
| `dashboards/strmon-redis-streams.json` | Full view: stream length, entries added, groups, per-group pending/lag/consumers, tables. Variables: Prometheus datasource, `stream`, `group` (regex `.*` when “All”). |
| `dashboards/strmon-summary.json` | Compact stats: max lag, sum pending, stream count, sum lengths, top-10 lag series. |

After import, open the dashboard and choose your **Prometheus** datasource if prompted. Dashboards include a **Redis** variable (`redis_addr`, label `redis` on metrics, value `host:port`). **Stream** / **Group** variables depend on the selected Redis instance(s). At least one scrape with data is required for variables to populate.
