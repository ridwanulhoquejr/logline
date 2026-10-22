package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"logline/internal/config"
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

	db, err := openDB(cfg)
	if err != nil {
		return err
	}
	defer db.Close()

	logger.Info("database connected",
		slog.Int("max_open_conns", cfg.DBMaxConns),
		slog.Int("max_idle_conns", cfg.DBMaxIdle),
	)

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

func openDB(cfg config.Config) (*sql.DB, error) {

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("opening database, %w", err)
	}

	db.SetMaxOpenConns(cfg.DBMaxConns)
	db.SetMaxIdleConns(cfg.DBMaxIdle)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connecting to database, %w", err)
	}

	return db, nil
}
