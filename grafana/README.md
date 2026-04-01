# Grafana dashboards (strmon)

Import JSON from **Dashboards → New → Import → Upload JSON file** (Grafana 9+).

**Prerequisites:** Prometheus scrapes strmon (`/metrics`), e.g. job `redis-stream-mon` in `prometheus.yml`.

| File | Purpose |
|------|---------|
| `dashboards/strmon-redis-streams.json` | Full view: stream length, entries added, groups, per-group pending/lag/consumers, tables. Variables: Prometheus datasource, `stream`, `group` (regex `.*` when “All”). |
| `dashboards/strmon-summary.json` | Compact stats: max lag, sum pending, stream count, sum lengths, top-10 lag series. |

After import, open the dashboard and choose your **Prometheus** datasource if prompted. Variable queries use `label_values(redis_stream_length, stream)` and `label_values(redis_stream_group_lag, group)`; they need at least one scrape with data.
