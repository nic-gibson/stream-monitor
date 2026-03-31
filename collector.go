package main

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

type streamCollector struct {
	rdb         *redis.Client
	mu          sync.RWMutex
	gauges      *streamGauges
	prevStreams map[string]bool
	prevGroups  map[string]bool
}

type streamGauges struct {
	streamLength     *prometheus.GaugeVec
	streamGroups     *prometheus.GaugeVec
	streamEntriesAdd *prometheus.GaugeVec
	groupConsumers   *prometheus.GaugeVec
	groupPending     *prometheus.GaugeVec
	groupLag         *prometheus.GaugeVec
}

func newStreamCollector(rdb *redis.Client) *streamCollector {
	return &streamCollector{
		rdb:         rdb,
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

	c.mu.Lock()
	defer c.mu.Unlock()

	seenStreams := make(map[string]bool)
	seenGroups := make(map[string]bool)

	for _, stream := range streams {
		seenStreams[stream] = true

		info, err := c.rdb.XInfoStream(ctx, stream).Result()
		if err != nil {
			if !strings.Contains(err.Error(), "no such key") {
				log.Error().Err(err).Str("stream", stream).Msg("stream info")
			}
			continue
		}

		c.gauges.streamLength.WithLabelValues(stream).Set(float64(info.Length))
		c.gauges.streamEntriesAdd.WithLabelValues(stream).Set(float64(info.EntriesAdded))

		groups, err := c.rdb.XInfoGroups(ctx, stream).Result()
		if err != nil {
			log.Error().Err(err).Str("stream", stream).Msg("stream consumer groups")
			c.gauges.streamGroups.WithLabelValues(stream).Set(0)
			continue
		}

		c.gauges.streamGroups.WithLabelValues(stream).Set(float64(len(groups)))

		for _, g := range groups {
			key := stream + "\x00" + g.Name
			seenGroups[key] = true
			c.gauges.groupConsumers.WithLabelValues(stream, g.Name).Set(float64(g.Consumers))
			c.gauges.groupPending.WithLabelValues(stream, g.Name).Set(float64(g.Pending))
			c.gauges.groupLag.WithLabelValues(stream, g.Name).Set(float64(g.Lag))
		}
	}

	c.removeStaleMetrics(seenStreams, seenGroups)
	c.prevStreams = seenStreams
	c.prevGroups = seenGroups
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
