package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

type Config struct {
	Port          string
	DatabaseURL   string
	RedisAddr     string
	WebhookSecret string
	CPFKey        string
	JWTJWKSURL    string
	OTELEndpoint  string
}

func Load() (*Config, error) {
	cfg := &Config{
		Port:          envOr("PORT", "8080"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		RedisAddr:     os.Getenv("REDIS_ADDR"),
		WebhookSecret: os.Getenv("WEBHOOK_SECRET"),
		CPFKey:        os.Getenv("CPF_KEY"),
		JWTJWKSURL:   os.Getenv("JWT_JWKS_URL"),
		OTELEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	var errs []string

	if c.DatabaseURL == "" {
		errs = append(errs, "DATABASE_URL is required")
	}

	if c.RedisAddr == "" {
		errs = append(errs, "REDIS_ADDR is required")
	}

	if c.WebhookSecret == "" {
		errs = append(errs, "WEBHOOK_SECRET is required")
	}

	if c.CPFKey == "" {
		errs = append(errs, "CPF_KEY is required")
	}

	if c.JWTJWKSURL == "" {
		errs = append(errs, "JWT_JWKS_URL is required")
	} else if _, err := url.ParseRequestURI(c.JWTJWKSURL); err != nil {
		errs = append(errs, "JWT_JWKS_URL must be a valid URL")
	}

	if len(errs) > 0 {
		return fmt.Errorf("configuration errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
