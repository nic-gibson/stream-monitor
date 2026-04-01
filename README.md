# stream-mon

A Go application that continuously monitors Redis streams and exposes metrics in Prometheus format via HTTP.

## Features

- Connects to Redis and discovers all streams using `SCAN TYPE stream`
- Periodically fetches stream info (`XINFO STREAM`) and consumer group info (`XINFO GROUPS`)
- Serves Prometheus metrics at `/metrics`
- Graceful shutdown on SIGINT (Ctrl+C) or SIGTERM

## Requirements

- Go 1.21+
- Redis 6.0+ (streams, `SCAN TYPE`, and optional ACL username/password)

## Build

```bash
go mod download
go build -o stream-mon .
```

## Usage

```bash
# Basic usage - connect to localhost Redis, poll every 30s, serve metrics on :9090
./stream-mon

# Connect to a remote Redis instance
./stream-mon --redis-addr redis.example.com:6379

# With password only
./stream-mon --redis-addr redis.example.com:6379 --redis-password mypassword

# With ACL username and password
./stream-mon --redis-addr redis.example.com:6379 --redis-user default --redis-password mypassword

# Custom poll interval and listen address
./stream-mon --poll-interval 10s --listen-addr :8080

# All options
./stream-mon --help
```

## Command Line Options

| Flag | Default | Description |
|------|---------|-------------|
| `--redis-addr` | `localhost:6379` | Redis server address (host:port) |
| `--redis-user` | `""` | Redis username for ACL authentication |
| `--redis-password` | `""` | Redis password |
| `--poll-interval` | `30s` | Interval between stream scans |
| `--listen-addr` | `:9090` | HTTP address for metrics and health endpoints |
| `--filter` | `.+` | Stream name filter | 

## Prometheus Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `redis_stream_length` | Gauge | `redis`, `stream` | Number of entries in the stream (`redis` = monitored instance `host:port`) |
| `redis_stream_consumer_groups` | Gauge | `redis`, `stream` | Number of consumer groups |
| `redis_stream_entries_added_total` | Gauge | `redis`, `stream` | Total entries ever added |
| `redis_stream_group_consumers` | Gauge | `redis`, `stream`, `group` | Consumers in the group |
| `redis_stream_group_pending` | Gauge | `redis`, `stream`, `group` | Pending (unacked) entries |
| `redis_stream_group_lag` | Gauge | `redis`, `stream`, `group` | Entries not yet delivered |

## Prometheus Configuration

Add to your `prometheus.yml`:

```yaml
scrape_configs:
  - job_name: 'redis-stream-mon'
    static_configs:
      - targets: ['localhost:9090']
    scrape_interval: 15s
```

## Dependencies

- [go-redis](https://github.com/redis/go-redis) - Redis client
- [Viper](https://github.com/spf13/viper) - Configuration (optional `config.yaml`, `STRMON_*` env vars, flags)
- [pflag](https://github.com/spf13/pflag) - Command-line flags (bound via Viper)
- [Prometheus client_golang](https://github.com/prometheus/client_golang) - Metrics exposition
