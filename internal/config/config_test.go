package config_test

import (
	"testing"

	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/config"
)

func validEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://app:app@localhost:5432/notifications")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("CPF_KEY", "cpf-key")
	t.Setenv("JWT_JWKS_URL", "http://localhost:8080/default/.well-known/jwks.json")
}

func TestLoad_Valid(t *testing.T) {
	validEnv(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.WebhookSecret != "webhook-secret" {
		t.Errorf("WebhookSecret = %q, want %q", cfg.WebhookSecret, "webhook-secret")
	}
	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want default %q", cfg.Port, "8080")
	}
}

func TestLoad_MissingRequired(t *testing.T) {
	vars := []string{"DATABASE_URL", "REDIS_ADDR", "WEBHOOK_SECRET", "CPF_KEY", "JWT_JWKS_URL"}
	for _, v := range vars {
		t.Setenv(v, "")
	}

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when all required vars are missing")
	}
}

func TestLoad_EachMissingVar(t *testing.T) {
	cases := []struct {
		name    string
		unset   string
	}{
		{"missing DATABASE_URL", "DATABASE_URL"},
		{"missing REDIS_ADDR", "REDIS_ADDR"},
		{"missing WEBHOOK_SECRET", "WEBHOOK_SECRET"},
		{"missing CPF_KEY", "CPF_KEY"},
		{"missing JWT_JWKS_URL", "JWT_JWKS_URL"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			validEnv(t)
			t.Setenv(tc.unset, "")

			_, err := config.Load()
			if err == nil {
				t.Fatalf("expected error when %s is missing", tc.unset)
			}
		})
	}
}

func TestLoad_InvalidJWKSURL(t *testing.T) {
	validEnv(t)
	t.Setenv("JWT_JWKS_URL", "not-a-url")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error for malformed JWT_JWKS_URL")
	}
}

func TestLoad_CustomPort(t *testing.T) {
	validEnv(t)
	t.Setenv("PORT", "9090")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != "9090" {
		t.Errorf("Port = %q, want %q", cfg.Port, "9090")
	}
}
