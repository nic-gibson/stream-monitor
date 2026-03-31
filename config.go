package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

type config struct {
	Redis        redisConfig `mapstructure:"redis"`
	PollInterval time.Duration `mapstructure:"poll_interval"`
	ListenAddr   string        `mapstructure:"listen_addr"`
}

type redisConfig struct {
	Addr         string `mapstructure:"addr"`
	User         string `mapstructure:"user"`
	Password     string `mapstructure:"password"`
	TLS          bool   `mapstructure:"tls"`
	TLSInsecure  bool   `mapstructure:"tls_insecure"`
	TLSClientCert string `mapstructure:"tls_client_cert"`
	TLSClientKey  string `mapstructure:"tls_client_key"`
	TLSCACert     string `mapstructure:"tls_ca_cert"`
}

func loadConfig() (*config, error) {
	v := viper.New()

	v.SetDefault("redis.addr", "localhost:6379")
	v.SetDefault("redis.user", "")
	v.SetDefault("redis.password", "")
	v.SetDefault("redis.tls", false)
	v.SetDefault("redis.tls_insecure", false)
	v.SetDefault("redis.tls_client_cert", "")
	v.SetDefault("redis.tls_client_key", "")
	v.SetDefault("redis.tls_ca_cert", "")
	v.SetDefault("poll_interval", 30*time.Second)
	v.SetDefault("listen_addr", ":9090")

	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("read config file: %w", err)
		}
	}

	v.SetEnvPrefix("STRMON")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	pflag.String("redis-addr", "localhost:6379", "Redis server address (host:port)")
	pflag.String("redis-user", "", "Redis username (optional, for ACL auth)")
	pflag.String("redis-password", "", "Redis password (optional)")
	pflag.Duration("poll-interval", 30*time.Second, "Interval between Redis stream scans")
	pflag.String("listen-addr", ":9090", "HTTP listen address for Prometheus metrics")
	pflag.Bool("redis-tls", false, "Connect to Redis over TLS")
	pflag.Bool("redis-tls-insecure", false, "Skip TLS certificate verification (insecure)")
	pflag.String("redis-tls-client-cert", "", "Path to PEM file with Redis client certificate (requires --redis-tls-client-key)")
	pflag.String("redis-tls-client-key", "", "Path to PEM file with Redis client private key (requires --redis-tls-client-cert)")
	pflag.String("redis-tls-ca-cert", "", "Path to PEM file with trusted CA certificate(s) for Redis server verification")

	if err := v.BindPFlag("redis.addr", pflag.Lookup("redis-addr")); err != nil {
		return nil, err
	}
	if err := v.BindPFlag("redis.user", pflag.Lookup("redis-user")); err != nil {
		return nil, err
	}
	if err := v.BindPFlag("redis.password", pflag.Lookup("redis-password")); err != nil {
		return nil, err
	}
	if err := v.BindPFlag("redis.tls", pflag.Lookup("redis-tls")); err != nil {
		return nil, err
	}
	if err := v.BindPFlag("redis.tls_insecure", pflag.Lookup("redis-tls-insecure")); err != nil {
		return nil, err
	}
	if err := v.BindPFlag("redis.tls_client_cert", pflag.Lookup("redis-tls-client-cert")); err != nil {
		return nil, err
	}
	if err := v.BindPFlag("redis.tls_client_key", pflag.Lookup("redis-tls-client-key")); err != nil {
		return nil, err
	}
	if err := v.BindPFlag("redis.tls_ca_cert", pflag.Lookup("redis-tls-ca-cert")); err != nil {
		return nil, err
	}
	if err := v.BindPFlag("poll_interval", pflag.Lookup("poll-interval")); err != nil {
		return nil, err
	}
	if err := v.BindPFlag("listen_addr", pflag.Lookup("listen-addr")); err != nil {
		return nil, err
	}

	pflag.Parse()

	var cfg config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	if err := validateRedisTLSOptions(cfg.Redis); err != nil {
		return nil, err
	}

	return &cfg, nil
}
