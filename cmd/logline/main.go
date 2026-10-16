package main

import (
	"fmt"
	"log/slog"
	"logline/internal/config"
	"logline/internal/server"
	"net/http"
	"os"
)

func main() {

	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load configuration error %v\n", err)
		os.Exit(1)
	}

	srv := server.New(cfg)

	addr := fmt.Sprintf(":%d", cfg.Port)
	slog.Info("logline starting",
		slog.String("addr", addr),
		slog.String("env", cfg.Env),
	)

	//? The below code was written in previous lesson
	if err := http.ListenAndServe(addr, srv); err != nil {
		slog.Error("server stopped",
			slog.String("error", err.Error()),
		)
		os.Exit(1)
	}
}
