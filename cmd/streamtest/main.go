// Streamtest is a load generator: 20 Redis streams, each with independent write rate
// and three consumer groups reading at combined rates similar (but not identical) to write.
// Uses the same Redis flags / config.yaml / STRMON_* env as strmon.
package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	numStreams      = 20
	groupsPerStream = 3
	streamPrefix    = "strmon:loadtest:"
	trimIntervalMin = 30 * time.Second
)

func main() {
	log.Logger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).With().Timestamp().Logger()

	rc, err := loadRedisConfig()
	if err != nil {
		log.Fatal().Err(err).Msg("config")
	}

	if rc.TLS && rc.TLSInsecure {
		log.Warn().Msg("Redis TLS certificate verification is disabled (--redis-tls-insecure); connections are vulnerable to man-in-the-middle attacks")
	}

	opts, err := redisOptions(rc)
	if err != nil {
		log.Fatal().Err(err).Msg("configure Redis TLS")
	}
	rdb := redis.NewClient(opts)
	defer rdb.Close()

	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatal().Err(err).Str("addr", rc.Addr).Msg("connect to Redis")
	}
	log.Info().Str("addr", rc.Addr).Msg("streamtest connected (Ctrl+C to stop)")

	if err := setupStreams(ctx, rdb); err != nil {
		log.Fatal().Err(err).Msg("setup streams and consumer groups")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	for i := range numStreams {
		key := fmt.Sprintf("%s%d", streamPrefix, i)

		writeTPS := 1.5 + rand.Float64()*6.5 // ~1.5–8 msg/s per stream
		readTotalTPS := writeTPS * (0.88 + rand.Float64()*0.24)
		weights := make([]float64, groupsPerStream)
		var sum float64
		for g := range groupsPerStream {
			weights[g] = 0.4 + rand.Float64()*1.2
			sum += weights[g]
		}
		readPerGroup := make([]float64, groupsPerStream)
		for g := range groupsPerStream {
			readPerGroup[g] = readTotalTPS * weights[g] / sum
		}

		log.Info().
			Str("stream", key).
			Float64("write_tps", writeTPS).
			Float64("read_total_tps", readTotalTPS).
			Interface("read_tps_per_group", readPerGroup).
			Msg("stream rates")

		wg.Add(1)
		go func() {
			defer wg.Done()
			runWriter(ctx, rdb, key, writeTPS)
		}()

		for g := range groupsPerStream {
			group := fmt.Sprintf("cg%d", g)
			tps := readPerGroup[g]
			wg.Add(1)
			go func(group string, tps float64) {
				defer wg.Done()
				runReader(ctx, rdb, key, group, fmt.Sprintf("c-%s", group), tps)
			}(group, tps)
		}
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		runTrimmer(ctx, rdb)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Info().Msg("stopping…")
	cancel()
	wg.Wait()
	log.Info().Msg("done")
}

// runTrimmer periodically XTRIMs each stream (random sleep 30–60s between cycles).
func runTrimmer(ctx context.Context, rdb *redis.Client) {
	for {
		wait := trimIntervalMin + time.Duration(rand.IntN(31))*time.Second
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		for i := range numStreams {
			key := fmt.Sprintf("%s%d", streamPrefix, i)
			n, err := trimStreamReadHistory(ctx, rdb, key)
			if err != nil {
				log.Warn().Err(err).Str("stream", key).Msg("stream trim")
				continue
			}
			if n > 0 {
				log.Info().Str("stream", key).Int64("entries_removed", n).Msg("XTRIM MINID")
			}
		}
	}
}

// trimStreamReadHistory removes stream entries with IDs strictly below the minimum
// last-delivered-id across consumer groups (entries at or before the slowest group's
// last delivery frontier).
func trimStreamReadHistory(ctx context.Context, rdb *redis.Client, stream string) (int64, error) {
	groups, err := rdb.XInfoGroups(ctx, stream).Result()
	if err != nil {
		return 0, err
	}
	if len(groups) == 0 {
		return 0, nil
	}

	minID := ""
	for _, g := range groups {
		id := g.LastDeliveredID
		if id == "" || id == "0-0" {
			continue
		}
		if minID == "" || strings.Compare(id, minID) < 0 {
			minID = id
		}
	}
	if minID == "" {
		return 0, nil
	}

	return rdb.XTrimMinID(ctx, stream, minID).Result()
}

func setupStreams(ctx context.Context, client *redis.Client) error {
	for i := range numStreams {
		key := fmt.Sprintf("%s%d", streamPrefix, i)

		if _, err := client.XAdd(ctx, &redis.XAddArgs{
			Stream: key,
			Values: map[string]interface{}{"_seed": "1"},
		}).Result(); err != nil {
			return fmt.Errorf("XADD %s: %w", key, err)
		}

		for g := range groupsPerStream {
			group := fmt.Sprintf("cg%d", g)
			err := client.XGroupCreate(ctx, key, group, "0").Err()
			if err != nil && !strings.Contains(strings.ToUpper(err.Error()), "BUSYGROUP") {
				return fmt.Errorf("XGROUP CREATE %s %s: %w", key, group, err)
			}
		}
	}
	return nil
}

func runWriter(ctx context.Context, rdb *redis.Client, stream string, tps float64) {
	interval := time.Duration(float64(time.Second) / tps)
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var n int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n++
			_, err := rdb.XAdd(ctx, &redis.XAddArgs{
				Stream: stream,
				Values: map[string]interface{}{
					"n": fmt.Sprintf("%d", n),
					"t": fmt.Sprintf("%d", time.Now().UnixNano()),
				},
			}).Result()
			if err != nil && ctx.Err() == nil {
				log.Error().Err(err).Str("stream", stream).Msg("XADD")
			}
		}
	}
}

func runReader(ctx context.Context, rdb *redis.Client, stream, group, consumer string, tps float64) {
	delay := time.Duration(float64(time.Second) / tps)
	if delay < 100*time.Microsecond {
		delay = 100 * time.Microsecond
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		res, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    group,
			Consumer: consumer,
			Streams:  []string{stream, ">"},
			Count:    64,
			Block:    500 * time.Millisecond,
		}).Result()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Error().Err(err).Str("stream", stream).Str("group", group).Msg("XREADGROUP")
			time.Sleep(delay)
			continue
		}

		var ids []string
		for _, xr := range res {
			for _, msg := range xr.Messages {
				ids = append(ids, msg.ID)
			}
		}
		if len(ids) > 0 {
			if err := rdb.XAck(ctx, stream, group, ids...).Err(); err != nil && ctx.Err() == nil {
				log.Error().Err(err).Str("stream", stream).Msg("XACK")
			}
		}

		time.Sleep(delay)
	}
}
