// Command qrsvc serves static QR codes as SVG. See README.md.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/ipedrazas/qrcode/internal/httpapi"
	"github.com/ipedrazas/qrcode/internal/qr"
)

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 10 * time.Second
	idleTimeout       = 60 * time.Second
	// Large enough for a maximum-length URL fully percent-encoded (3×2048
	// bytes) plus ordinary headers.
	maxHeaderBytes = 16 << 10
	// How long in-flight requests get to finish after SIGINT/SIGTERM.
	drainTimeout = 15 * time.Second
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "qrsvc:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	var limiter *httpapi.RateLimiter
	if cfg.RateLimitRPS > 0 {
		limiter = httpapi.NewRateLimiter(cfg.RateLimitRPS, cfg.RateLimitBurst)
	}

	srv := &http.Server{
		Addr: net.JoinHostPort("", strconv.Itoa(cfg.Port)),
		Handler: httpapi.NewHandler(httpapi.Config{
			Encoder:   qr.NewEncoder(),
			Logger:    logger,
			MaxURLLen: cfg.MaxURLLen,
			Limiter:   limiter,
			LogURLs:   cfg.LogURLs,
		}),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("listening",
			slog.String("addr", srv.Addr),
			slog.Float64("rate_limit_rps", cfg.RateLimitRPS),
			slog.Int("rate_limit_burst", cfg.RateLimitBurst),
			slog.Int("max_url_len", cfg.MaxURLLen),
			slog.Bool("log_urls", cfg.LogURLs),
		)
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}
	stop() // a second signal kills the process immediately

	logger.Info("shutting down", slog.String("drain_timeout", drainTimeout.String()))
	shutdownCtx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	logger.Info("stopped")
	return nil
}
