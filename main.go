package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	log.Logger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).With().Timestamp().Logger()

	cfg, err := loadConfig()
	if err != nil {
		log.Fatal().Err(err).Msg("load config")
	}

	if cfg.Redis.TLS && cfg.Redis.TLSInsecure {
		log.Warn().Msg("Redis TLS certificate verification is disabled (--redis-tls-insecure); connections are vulnerable to man-in-the-middle attacks")
	}

	tlsCfg, err := buildRedisTLSConfig(cfg.Redis)
	if err != nil {
		log.Fatal().Err(err).Msg("configure Redis TLS")
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:      cfg.Redis.Addr,
		Username:  cfg.Redis.User,
		Password:  cfg.Redis.Password,
		TLSConfig: tlsCfg,
	})
	defer rdb.Close()

	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatal().Err(err).Str("addr", cfg.Redis.Addr).Msg("connect to Redis")
	}
	log.Info().Str("addr", cfg.Redis.Addr).Msg("connected to Redis")

	collector := newStreamCollector(rdb)
	prometheus.MustRegister(collector)

	http.Handle("/metrics", promhttp.Handler())

	server := &http.Server{Addr: cfg.ListenAddr}
	go func() {
		log.Info().Str("listen", cfg.ListenAddr).Msg("serving Prometheus metrics")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("HTTP server")
		}
	}()

	go collector.run(ctx, cfg.PollInterval)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Info().Msg("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("HTTP server shutdown")
	}
}
