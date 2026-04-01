package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	"github.com/redis/go-redis/v9"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// redisConfig mirrors the main application's Redis settings (same flags, config.yaml, STRMON_* env).
type redisConfig struct {
	Addr          string `mapstructure:"addr"`
	User          string `mapstructure:"user"`
	Password      string `mapstructure:"password"`
	TLS           bool   `mapstructure:"tls"`
	TLSInsecure   bool   `mapstructure:"tls_insecure"`
	TLSClientCert string `mapstructure:"tls_client_cert"`
	TLSClientKey  string `mapstructure:"tls_client_key"`
	TLSCACert     string `mapstructure:"tls_ca_cert"`
}

func loadRedisConfig() (redisConfig, error) {
	v := viper.New()
	v.SetDefault("redis.addr", "localhost:6379")
	v.SetDefault("redis.user", "")
	v.SetDefault("redis.password", "")
	v.SetDefault("redis.tls", false)
	v.SetDefault("redis.tls_insecure", false)
	v.SetDefault("redis.tls_client_cert", "")
	v.SetDefault("redis.tls_client_key", "")
	v.SetDefault("redis.tls_ca_cert", "")

	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return redisConfig{}, fmt.Errorf("read config file: %w", err)
		}
	}

	v.SetEnvPrefix("STRMON")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	pflag.String("redis-addr", "localhost:6379", "Redis server address (host:port)")
	pflag.String("redis-user", "", "Redis username (optional, for ACL auth)")
	pflag.String("redis-password", "", "Redis password (optional)")
	pflag.Bool("redis-tls", false, "Connect to Redis over TLS")
	pflag.Bool("redis-tls-insecure", false, "Skip TLS certificate verification (insecure)")
	pflag.String("redis-tls-client-cert", "", "Path to PEM file with Redis client certificate (requires --redis-tls-client-key)")
	pflag.String("redis-tls-client-key", "", "Path to PEM file with Redis client private key (requires --redis-tls-client-cert)")
	pflag.String("redis-tls-ca-cert", "", "Path to PEM file with trusted CA certificate(s) for Redis server verification")

	if err := v.BindPFlag("redis.addr", pflag.Lookup("redis-addr")); err != nil {
		return redisConfig{}, err
	}
	if err := v.BindPFlag("redis.user", pflag.Lookup("redis-user")); err != nil {
		return redisConfig{}, err
	}
	if err := v.BindPFlag("redis.password", pflag.Lookup("redis-password")); err != nil {
		return redisConfig{}, err
	}
	if err := v.BindPFlag("redis.tls", pflag.Lookup("redis-tls")); err != nil {
		return redisConfig{}, err
	}
	if err := v.BindPFlag("redis.tls_insecure", pflag.Lookup("redis-tls-insecure")); err != nil {
		return redisConfig{}, err
	}
	if err := v.BindPFlag("redis.tls_client_cert", pflag.Lookup("redis-tls-client-cert")); err != nil {
		return redisConfig{}, err
	}
	if err := v.BindPFlag("redis.tls_client_key", pflag.Lookup("redis-tls-client-key")); err != nil {
		return redisConfig{}, err
	}
	if err := v.BindPFlag("redis.tls_ca_cert", pflag.Lookup("redis-tls-ca-cert")); err != nil {
		return redisConfig{}, err
	}

	pflag.Parse()

	var wrap struct {
		Redis redisConfig `mapstructure:"redis"`
	}
	if err := v.Unmarshal(&wrap); err != nil {
		return redisConfig{}, fmt.Errorf("unmarshal config: %w", err)
	}
	if err := validateRedisTLSOptions(wrap.Redis); err != nil {
		return redisConfig{}, err
	}
	return wrap.Redis, nil
}

func validateRedisTLSOptions(rc redisConfig) error {
	if rc.TLS {
		return nil
	}
	if rc.TLSInsecure || rc.TLSCACert != "" || rc.TLSClientCert != "" || rc.TLSClientKey != "" {
		return fmt.Errorf("TLS options (--redis-tls-insecure, --redis-tls-ca-cert, client cert) require --redis-tls")
	}
	return nil
}

func buildRedisTLSConfig(rc redisConfig) (*tls.Config, error) {
	if !rc.TLS {
		return nil, nil
	}

	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	if rc.TLSCACert != "" {
		pem, err := os.ReadFile(rc.TLSCACert)
		if err != nil {
			return nil, fmt.Errorf("read Redis TLS CA cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificates parsed from Redis TLS CA file %q", rc.TLSCACert)
		}
		cfg.RootCAs = pool
	}

	if rc.TLSClientCert != "" || rc.TLSClientKey != "" {
		if rc.TLSClientCert == "" || rc.TLSClientKey == "" {
			return nil, fmt.Errorf("Redis TLS client authentication requires both --redis-tls-client-cert and --redis-tls-client-key")
		}
		cert, err := tls.LoadX509KeyPair(rc.TLSClientCert, rc.TLSClientKey)
		if err != nil {
			return nil, fmt.Errorf("load Redis TLS client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}

	cfg.InsecureSkipVerify = rc.TLSInsecure

	return cfg, nil
}

func redisOptions(rc redisConfig) (*redis.Options, error) {
	tlsCfg, err := buildRedisTLSConfig(rc)
	if err != nil {
		return nil, err
	}
	return &redis.Options{
		Addr:      rc.Addr,
		Username:  rc.User,
		Password:  rc.Password,
		TLSConfig: tlsCfg,
	}, nil
}
