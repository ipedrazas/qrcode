package main

import (
	"log/slog"
	"strings"
	"testing"
)

func env(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func TestLoadConfigDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := loadConfig(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	want := config{Port: 8080, RateLimitRPS: 10, RateLimitBurst: 20, MaxURLLen: 2048, LogLevel: slog.LevelInfo}
	if cfg != want {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
}

func TestLoadConfigOverrides(t *testing.T) {
	t.Parallel()
	cfg, err := loadConfig(env(map[string]string{
		"PORT": "9000", "RATE_LIMIT_RPS": "0.5", "RATE_LIMIT_BURST": "3", "MAX_URL_LEN": "512", "LOG_LEVEL": "debug",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := config{Port: 9000, RateLimitRPS: 0.5, RateLimitBurst: 3, MaxURLLen: 512, LogLevel: slog.LevelDebug}
	if cfg != want {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
	if cfg, err := loadConfig(env(map[string]string{"RATE_LIMIT_RPS": "0"})); err != nil || cfg.RateLimitRPS != 0 {
		t.Fatalf("RATE_LIMIT_RPS=0 should disable limiting: %+v, %v", cfg, err)
	}
}

func TestLoadConfigInvalid(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"PORT":             "http",
		"RATE_LIMIT_RPS":   "-1",
		"RATE_LIMIT_BURST": "0",
		"MAX_URL_LEN":      "2954",
		"LOG_LEVEL":        "verbose",
	}
	for k, v := range cases {
		if _, err := loadConfig(env(map[string]string{k: v})); err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("%s=%s: err = %v", k, v, err)
		}
	}
	for _, bad := range []map[string]string{{"PORT": "0"}, {"PORT": "65536"}, {"RATE_LIMIT_RPS": "NaN"}, {"RATE_LIMIT_RPS": "+Inf"}, {"MAX_URL_LEN": "0"}} {
		if _, err := loadConfig(env(bad)); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	// All problems are reported at once.
	_, err := loadConfig(env(cases))
	for k := range cases {
		if err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("combined error does not mention %s: %v", k, err)
		}
	}
}
