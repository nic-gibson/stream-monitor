package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

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

func validateRedisTLSOptions(rc redisConfig) error {
	if rc.TLS {
		return nil
	}
	if rc.TLSInsecure || rc.TLSCACert != "" || rc.TLSClientCert != "" || rc.TLSClientKey != "" {
		return fmt.Errorf("TLS options (--redis-tls-insecure, --redis-tls-ca-cert, client cert) require --redis-tls")
	}
	return nil
}
