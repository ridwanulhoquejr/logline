package main

import (
	"fmt"
	"log"
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
	log.Printf("logline starting on %s (env=%s, log-level=%s)", addr, cfg.Env, cfg.LogLevel)

	//? The below code was written in previous lesson
	if err := http.ListenAndServe(addr, srv); err != nil {
		log.Fatal(err)
	}
}
