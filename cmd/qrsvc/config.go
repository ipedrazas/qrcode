package main

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"

	"github.com/ipedrazas/qrcode/internal/httpapi"
	"github.com/ipedrazas/qrcode/internal/qr"
)

type config struct {
	Port           int
	RateLimitRPS   float64 // 0 disables rate limiting
	RateLimitBurst int
	MaxURLLen      int
	LogLevel       slog.Level
}

// loadConfig reads configuration from the environment. Every variable is
// optional; invalid values are reported together rather than one at a time.
func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{
		Port:           8080,
		RateLimitRPS:   10,
		RateLimitBurst: 20,
		MaxURLLen:      httpapi.DefaultMaxURLLen,
		LogLevel:       slog.LevelInfo,
	}
	// No URL longer than this can be encoded at any EC level, so a higher
	// limit would only move the rejection from url_too_long to capacity_exceeded.
	maxURLLenCeiling := qr.MaxPayloadBytes(qr.ECLow)

	var errs []error
	if v := getenv("PORT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			errs = append(errs, fmt.Errorf("PORT must be an integer from 1 to 65535, got %q", v))
		} else {
			cfg.Port = n
		}
	}
	if v := getenv("RATE_LIMIT_RPS"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 || math.IsInf(f, 0) || math.IsNaN(f) {
			errs = append(errs, fmt.Errorf("RATE_LIMIT_RPS must be a non-negative number (0 disables rate limiting), got %q", v))
		} else {
			cfg.RateLimitRPS = f
		}
	}
	if v := getenv("RATE_LIMIT_BURST"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			errs = append(errs, fmt.Errorf("RATE_LIMIT_BURST must be a positive integer, got %q", v))
		} else {
			cfg.RateLimitBurst = n
		}
	}
	if v := getenv("MAX_URL_LEN"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxURLLenCeiling {
			errs = append(errs, fmt.Errorf("MAX_URL_LEN must be an integer from 1 to %d, got %q", maxURLLenCeiling, v))
		} else {
			cfg.MaxURLLen = n
		}
	}
	if v := getenv("LOG_LEVEL"); v != "" {
		if err := cfg.LogLevel.UnmarshalText([]byte(v)); err != nil {
			errs = append(errs, fmt.Errorf("LOG_LEVEL must be one of debug, info, warn or error, got %q", v))
		}
	}
	return cfg, errors.Join(errs...)
}
