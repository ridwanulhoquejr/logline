package main

import (
	"context"
	"fmt"
	"log/slog"
	"logline/internal/config"
	"logline/internal/database"
	"logline/internal/server"
	"logline/internal/store"
	"net/http"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stdout, "error: %+v\n", err)
		os.Exit(1)
	}
}

func run() error {

	cfg, err := config.LoadConfig()
	if err != nil {
		return err
	}

	logger := server.NewLogger(cfg.Env, cfg.LogLevel)

	db, err := database.Open(cfg.DatabaseURL, cfg.DBMaxConns, cfg.DBMaxIdle)
	if err != nil {
		return err
	}
	defer db.Close()

	logger.Info("database connected",
		slog.Int("max_open_conns", cfg.DBMaxConns),
		slog.Int("max_idle_conns", cfg.DBMaxIdle),
	)

	if err := database.Migrate(context.Background(), db); err != nil {
		return err
	}
	logger.Info("migration applied")

	logStore := store.New(db)
	bufferedStore := store.NewBufferedStore(logStore, 500, 500*time.Millisecond)
	defer bufferedStore.Close()

	srv := server.New(cfg, logger, logStore, bufferedStore)

	addr := fmt.Sprintf(":%d", cfg.Port)
	slog.Info("logline starting",
		slog.String("addr", addr),
		slog.String("env", cfg.Env),
	)

	return http.ListenAndServe(addr, srv)
}
