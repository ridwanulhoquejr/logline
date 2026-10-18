package config

import (
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	Port        int
	Env         string
	LogLevel    string
	DatabaseURL string
	DBMaxConns  int
	DBMaxIdle   int
}

func LoadConfig() (Config, error) {
	cfg := Config{
		Port:        4000,
		Env:         "development",
		LogLevel:    "info",
		DatabaseURL: "",
		DBMaxConns:  25,
		DBMaxIdle:   5,
	}

	cfg.loadEnv()

	if err := cfg.loadFlags(); err != nil {
		return Config{}, err
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c *Config) loadEnv() {

	// this will ensure to get the config variables from .env
	_ = godotenv.Load()

	if v := os.Getenv("LOGLINE_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			c.Port = p
		}
	}

	if v := os.Getenv("APP_ENV"); v != "" {
		c.Env = v
	}

	if v := os.Getenv("LOG_LEVEL"); v != "" {
		c.LogLevel = v
	}

	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		c.DatabaseURL = dsn
	}

	if dbMaxConn := os.Getenv("DB_MAX_CONNECTIONS"); dbMaxConn != "" {
		if dbMaxConnInt, err := strconv.Atoi(dbMaxConn); err == nil {
			c.DBMaxConns = dbMaxConnInt
		}
	}

	if dbMaxIdle := os.Getenv("DB_MAX_IDLE"); dbMaxIdle != "" {
		if dbMaxIdleInt, err := strconv.Atoi(dbMaxIdle); err == nil {
			c.DBMaxIdle = dbMaxIdleInt
		}
	}
}

func (c *Config) loadFlags() error {
	fs := flag.NewFlagSet("logline", flag.ContinueOnError)

	fs.IntVar(&c.Port, "port", c.Port, "HTTP server port")
	fs.StringVar(&c.Env, "env", c.Env, "Environment (development, production)")
	fs.StringVar(&c.LogLevel, "log-level", c.LogLevel, "Log level (debug, info, warn, error)")

	return fs.Parse(os.Args[1:])
}

func (c *Config) validate() error {

	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", c.Port)
	}

	if c.DatabaseURL == "" {
		return fmt.Errorf("DatabaseURL should not be empty")
	}

	if c.DBMaxConns < 1 || c.DBMaxIdle < 1 {
		return fmt.Errorf("DBMaxConns or DBMaxIdle must be greater than 0")
	}

	switch c.Env {
	case "development", "production":
		// valid
	default:
		return fmt.Errorf("environment must be `development` or `production`, got %q", c.Env)
	}

	switch c.LogLevel {
	case "debug", "warn", "error", "info":
		//valid
	default:
		return fmt.Errorf("log-level must be one of: `debug`, `info`, `warn`, `error` - got %q", c.LogLevel)
	}

	return nil
}
