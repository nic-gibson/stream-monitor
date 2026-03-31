package main

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// streamFetchConcurrency limits parallel Redis calls per collect (XINFO per stream).
const streamFetchConcurrency = 32

type streamFetchResult struct {
	stream    string
	infoOK    bool
	info      *redis.XInfoStream
	infoErr   error
	groupsOK  bool
	groups    []redis.XInfoGroup
	groupsErr error
}

type streamCollector struct {
	rdb         *redis.Client
	mu          sync.RWMutex
	gauges      *streamGauges
	prevStreams map[string]bool
	prevGroups  map[string]bool
	metricsLog  *zerolog.Logger
}

type groupMetricSnapshot struct {
	Name       string `json:"name"`
	Consumers  int64  `json:"consumers"`
	Pending    int64  `json:"pending"`
	Lag        int64  `json:"lag"`
}

type streamMetricSnapshot struct {
	Stream       string                `json:"stream"`
	Length       int64                 `json:"length"`
	EntriesAdded int64                 `json:"entries_added"`
	Groups       int                   `json:"groups"`
	GroupDetails []groupMetricSnapshot `json:"group_details,omitempty"`
}

type streamGauges struct {
	streamLength     *prometheus.GaugeVec
	streamGroups     *prometheus.GaugeVec
	streamEntriesAdd *prometheus.GaugeVec
	groupConsumers   *prometheus.GaugeVec
	groupPending     *prometheus.GaugeVec
	groupLag         *prometheus.GaugeVec
}

func newStreamCollector(rdb *redis.Client, metricsLog *zerolog.Logger) *streamCollector {
	return &streamCollector{
		rdb:         rdb,
		metricsLog:  metricsLog,
		prevStreams: make(map[string]bool),
		prevGroups:  make(map[string]bool),
		gauges: &streamGauges{
			streamLength: prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Name: "redis_stream_length",
					Help: "Number of entries in the Redis stream",
				},
				[]string{"stream"},
			),
			streamGroups: prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Name: "redis_stream_consumer_groups",
					Help: "Number of consumer groups for the stream",
				},
				[]string{"stream"},
			),
			streamEntriesAdd: prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Name: "redis_stream_entries_added_total",
					Help: "Total entries ever added to the stream",
				},
				[]string{"stream"},
			),
			groupConsumers: prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Name: "redis_stream_group_consumers",
					Help: "Number of consumers in the consumer group",
				},
				[]string{"stream", "group"},
			),
			groupPending: prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Name: "redis_stream_group_pending",
					Help: "Number of pending entries in the consumer group",
				},
				[]string{"stream", "group"},
			),
			groupLag: prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Name: "redis_stream_group_lag",
					Help: "Number of entries not yet delivered to the consumer group",
				},
				[]string{"stream", "group"},
			),
		},
	}
}

func (c *streamCollector) Describe(ch chan<- *prometheus.Desc) {
	c.gauges.streamLength.Describe(ch)
	c.gauges.streamGroups.Describe(ch)
	c.gauges.streamEntriesAdd.Describe(ch)
	c.gauges.groupConsumers.Describe(ch)
	c.gauges.groupPending.Describe(ch)
	c.gauges.groupLag.Describe(ch)
}

func (c *streamCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	c.gauges.streamLength.Collect(ch)
	c.gauges.streamGroups.Collect(ch)
	c.gauges.streamEntriesAdd.Collect(ch)
	c.gauges.groupConsumers.Collect(ch)
	c.gauges.groupPending.Collect(ch)
	c.gauges.groupLag.Collect(ch)
}

func (c *streamCollector) run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	c.collect(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.collect(ctx)
		}
	}
}

func (c *streamCollector) collect(ctx context.Context) {
	streams, err := c.findStreams(ctx)
	if err != nil {
		log.Error().Err(err).Msg("find streams")
		return
	}

	results := c.fetchAllStreams(ctx, streams)

	c.mu.Lock()
	defer c.mu.Unlock()

	seenStreams := make(map[string]bool)
	seenGroups := make(map[string]bool)

	var metricSnapshots []streamMetricSnapshot
	if c.metricsLog != nil {
		metricSnapshots = make([]streamMetricSnapshot, 0, len(results))
	}

	for _, r := range results {
		stream := r.stream
		seenStreams[stream] = true

		if !r.infoOK {
			if r.infoErr != nil && !strings.Contains(r.infoErr.Error(), "no such key") {
				log.Error().Err(r.infoErr).Str("stream", stream).Msg("stream info")
			}
			continue
		}

		info := r.info
		c.gauges.streamLength.WithLabelValues(stream).Set(float64(info.Length))
		c.gauges.streamEntriesAdd.WithLabelValues(stream).Set(float64(info.EntriesAdded))

		if !r.groupsOK {
			log.Error().Err(r.groupsErr).Str("stream", stream).Msg("stream consumer groups")
			c.gauges.streamGroups.WithLabelValues(stream).Set(0)
			if c.metricsLog != nil {
				metricSnapshots = append(metricSnapshots, streamMetricSnapshot{
					Stream:       stream,
					Length:       info.Length,
					EntriesAdded: info.EntriesAdded,
					Groups:       0,
				})
			}
			continue
		}

		groups := r.groups
		c.gauges.streamGroups.WithLabelValues(stream).Set(float64(len(groups)))

		var snap streamMetricSnapshot
		if c.metricsLog != nil {
			snap = streamMetricSnapshot{
				Stream:       stream,
				Length:       info.Length,
				EntriesAdded: info.EntriesAdded,
				Groups:       len(groups),
				GroupDetails: make([]groupMetricSnapshot, 0, len(groups)),
			}
		}

		for _, g := range groups {
			key := stream + "\x00" + g.Name
			seenGroups[key] = true
			c.gauges.groupConsumers.WithLabelValues(stream, g.Name).Set(float64(g.Consumers))
			c.gauges.groupPending.WithLabelValues(stream, g.Name).Set(float64(g.Pending))
			c.gauges.groupLag.WithLabelValues(stream, g.Name).Set(float64(g.Lag))
			if c.metricsLog != nil {
				snap.GroupDetails = append(snap.GroupDetails, groupMetricSnapshot{
					Name:      g.Name,
					Consumers: int64(g.Consumers),
					Pending:   int64(g.Pending),
					Lag:       g.Lag,
				})
			}
		}

		if c.metricsLog != nil {
			metricSnapshots = append(metricSnapshots, snap)
		}
	}

	c.removeStaleMetrics(seenStreams, seenGroups)
	c.prevStreams = seenStreams
	c.prevGroups = seenGroups

	c.emitMetricSnapshots(metricSnapshots)
}

func (c *streamCollector) fetchStreamData(ctx context.Context, stream string) streamFetchResult {
	r := streamFetchResult{stream: stream}
	info, err := c.rdb.XInfoStream(ctx, stream).Result()
	if err != nil {
		r.infoErr = err
		return r
	}
	r.infoOK = true
	r.info = info

	groups, err := c.rdb.XInfoGroups(ctx, stream).Result()
	if err != nil {
		r.groupsErr = err
		return r
	}
	r.groupsOK = true
	r.groups = groups
	return r
}

// fetchAllStreams runs XINFO STREAM / XINFO GROUPS for each name using a bounded worker pool.
func (c *streamCollector) fetchAllStreams(ctx context.Context, streams []string) []streamFetchResult {
	if len(streams) == 0 {
		return nil
	}

	workers := streamFetchConcurrency
	if len(streams) < workers {
		workers = len(streams)
	}

	jobs := make(chan string)
	var outMu sync.Mutex
	out := make([]streamFetchResult, 0, len(streams))

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for stream := range jobs {
				res := c.fetchStreamData(ctx, stream)
				outMu.Lock()
				out = append(out, res)
				outMu.Unlock()
			}
		}()
	}

	for _, s := range streams {
		jobs <- s
	}
	close(jobs)
	wg.Wait()

	return out
}

func (c *streamCollector) emitMetricSnapshots(streams []streamMetricSnapshot) {
	if c.metricsLog == nil {
		return
	}
	c.metricsLog.Info().
		Int("stream_count", len(streams)).
		Interface("streams", streams).
		Msg("redis stream metrics")
}

func (c *streamCollector) removeStaleMetrics(seenStreams map[string]bool, seenGroups map[string]bool) {
	for stream := range c.prevStreams {
		if !seenStreams[stream] {
			c.gauges.streamLength.DeleteLabelValues(stream)
			c.gauges.streamGroups.DeleteLabelValues(stream)
			c.gauges.streamEntriesAdd.DeleteLabelValues(stream)
		}
	}
	for key := range c.prevGroups {
		parts := strings.SplitN(key, "\x00", 2)
		if len(parts) != 2 {
			continue
		}
		stream, group := parts[0], parts[1]
		if !seenGroups[key] {
			c.gauges.groupConsumers.DeleteLabelValues(stream, group)
			c.gauges.groupPending.DeleteLabelValues(stream, group)
			c.gauges.groupLag.DeleteLabelValues(stream, group)
		}
	}
}

func (c *streamCollector) findStreams(ctx context.Context) ([]string, error) {
	var streams []string
	var cursor uint64

	for {
		keys, nextCursor, err := c.rdb.ScanType(ctx, cursor, "*", 100, "stream").Result()
		if err != nil {
			return nil, err
		}

		streams = append(streams, keys...)
		cursor = nextCursor

		if cursor == 0 {
			break
		}
	}

	return streams, nil
}
