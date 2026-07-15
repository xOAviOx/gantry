// Package config loads controld's configuration from the environment, with a
// minimal built-in .env loader so `go run` works in dev without exporting vars.
package config

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the fully-resolved runtime configuration. No package-level state;
// callers inject this everywhere (SPEC.md §18).
type Config struct {
	DatabaseURL   string
	ControldAddr  string   // listen addr, e.g. ":8080"
	ControldPort  int      // parsed from ControldAddr, used in Caddy upstreams
	WebPort       int      // Next.js dev port for Caddy upstream
	HTTPPort      int      // public port Caddy publishes
	CaddyAdmin    string   // e.g. http://127.0.0.1:2019
	CaddyOrigins  []string // admin origins baked into every rendered config
	HostInternal  string   // hostname Caddy uses to reach host processes
	AppsNetwork   string   // docker network app containers join
	AdminToken    string   // bearer token for API/dashboard
	WebhookSecret string   // github webhook HMAC secret
	MasterKeyB64  string   // base64 32-byte AES key
	DockerHost    string   // optional docker endpoint override
	Workers       int
	BuildTimeout  time.Duration
	KeepImages    int
	BuilderKeep   string
	LogLevel      string
}

// Load reads deploy/.env (if present and vars are unset) then the process env.
func Load() (Config, error) {
	loadDotEnv(getenv("GANTRY_ENV_FILE", "deploy/.env"))

	c := Config{
		DatabaseURL:   getenv("DATABASE_URL", ""),
		ControldAddr:  getenv("CONTROLD_ADDR", ":8080"),
		HTTPPort:      getenvInt("GANTRY_HTTP_PORT", 80),
		CaddyAdmin:    getenv("CADDY_ADMIN", "http://127.0.0.1:2019"),
		HostInternal:  getenv("GANTRY_HOST_INTERNAL", "host.docker.internal"),
		AppsNetwork:   getenv("GANTRY_APPS_NETWORK", "gantry-apps"),
		AdminToken:    getenv("ADMIN_TOKEN", ""),
		WebhookSecret: getenv("GITHUB_WEBHOOK_SECRET", ""),
		MasterKeyB64:  getenv("GANTRY_MASTER_KEY", ""),
		DockerHost:    getenv("DOCKER_HOST", ""),
		Workers:       getenvInt("GANTRY_WORKERS", 2),
		KeepImages:    getenvInt("GANTRY_KEEP_IMAGES", 3),
		BuilderKeep:   getenv("GANTRY_BUILDER_KEEP_STORAGE", "20GB"),
		LogLevel:      getenv("GANTRY_LOG_LEVEL", "info"),
	}
	c.ControldPort = portFromAddr(c.ControldAddr, 8080)
	c.WebPort = portFromAddr(getenv("WEB_ADDR", ":3000"), 3000)
	c.CaddyOrigins = []string{"127.0.0.1:2019", "localhost:2019", "[::1]:2019", "gantry-caddy:2019"}

	bt, err := time.ParseDuration(getenv("GANTRY_BUILD_TIMEOUT", "15m"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid GANTRY_BUILD_TIMEOUT: %w", err)
	}
	c.BuildTimeout = bt

	if c.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required (set it in deploy/.env)")
	}
	return c, nil
}

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

// portFromAddr extracts the numeric port from an addr like ":8080" or "1.2.3.4:8080".
func portFromAddr(addr string, def int) int {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return def
	}
	n, err := strconv.Atoi(portStr)
	if err != nil {
		return def
	}
	return n
}

// loadDotEnv sets any KEY it finds in path that isn't already present in the
// environment. Silently no-ops if the file is missing. Not a real dotenv lib —
// just enough for dev (KEY=VALUE, # comments, optional surrounding quotes).
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}
