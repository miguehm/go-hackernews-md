// Package config loads and validates the server configuration from
// environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultCORSOrigins is the allowlist used when CORS_ORIGINS is not set.
var DefaultCORSOrigins = []string{
	"web",
	"http://localhost:5173",
	"https://localhost",
	"capacitor://localhost",
}

const (
	maxTopN           = 500 // HN /topstories returns at most 500 ids
	maxConcurrencyCap = 64
)

// Config is the fully validated server configuration.
type Config struct {
	Port string

	RefreshInterval time.Duration
	ScrapeTimeout   time.Duration
	HNTimeout       time.Duration
	TopN            int
	MaxConcurrency  int
	MaxBodySize     int64

	APIKeys       []string
	ProtectHealth bool
	CORSOrigins   []string

	DataDir                 string
	TodoFile                string
	TodoMaxTasks            int
	TodoAllowInsecureWrites bool
	TodoLocation            *time.Location
	PublicBaseURL           string

	LogLevel  slog.Level
	LogFormat string

	// Warnings lists tolerated problems (a lenient variable fell back to its
	// default). They are returned instead of logged because the logger is
	// itself built from this configuration.
	Warnings []string
}

// AuthEnabled reports whether API key authentication is active.
func (c Config) AuthEnabled() bool { return len(c.APIKeys) > 0 }

type loader struct {
	getenv   func(string) string
	errs     []error
	warnings []string
}

// Load reads the configuration through getenv (os.Getenv in production).
// Every invalid strict variable is reported, joined into one error.
func Load(getenv func(string) string) (Config, error) {
	l := &loader{getenv: getenv}
	cfg := Config{
		Port:                    l.str("PORT", "8982"),
		RefreshInterval:         l.duration("REFRESH_INTERVAL", "30m"),
		ScrapeTimeout:           l.duration("SCRAPE_TIMEOUT", "10s"),
		HNTimeout:               l.duration("HN_TIMEOUT", "10s"),
		TopN:                    l.intRange("TOP_N", 20, 1, maxTopN),
		MaxConcurrency:          l.intRange("MAX_CONCURRENCY", 6, 1, maxConcurrencyCap),
		MaxBodySize:             5 * 1024 * 1024,
		APIKeys:                 ParseList(l.getenv("API_KEYS")),
		ProtectHealth:           l.boolean("PROTECT_HEALTH", false),
		CORSOrigins:             ParseCORSOrigins(l.getenv("CORS_ORIGINS")),
		DataDir:                 l.str("DATA_DIR", "./data"),
		TodoMaxTasks:            l.lenientInt("TODO_MAX_TASKS", 1000),
		TodoAllowInsecureWrites: l.boolean("TODO_ALLOW_INSECURE_WRITES", false),
		TodoLocation:            l.location("TODO_TIMEZONE"),
		PublicBaseURL:           l.getenv("PUBLIC_BASE_URL"),
		LogLevel:                l.logLevel("LOG_LEVEL"),
		LogFormat:               l.logFormat("LOG_FORMAT"),
	}
	cfg.TodoFile = l.str("TODO_FILE", filepath.Join(cfg.DataDir, "todos.json"))
	cfg.Warnings = l.warnings
	if len(l.errs) > 0 {
		return Config{}, errors.Join(l.errs...)
	}
	return cfg, nil
}

func (l *loader) str(key, def string) string {
	if v := l.getenv(key); v != "" {
		return v
	}
	return def
}

func (l *loader) fail(format string, args ...any) {
	l.errs = append(l.errs, fmt.Errorf(format, args...))
}

func (l *loader) warn(format string, args ...any) {
	l.warnings = append(l.warnings, fmt.Sprintf(format, args...))
}

// duration requires a strictly positive value: zero would disable HTTP
// client timeouts or panic the refresh ticker.
func (l *loader) duration(key, def string) time.Duration {
	raw := strings.TrimSpace(l.str(key, def))
	d, err := time.ParseDuration(raw)
	if err != nil {
		l.fail("invalid %s %q: %w", key, raw, err)
		return 0
	}
	if d <= 0 {
		l.fail("invalid %s %q: must be greater than zero", key, raw)
		return 0
	}
	return d
}

func (l *loader) intRange(key string, def, minimum, maximum int) int {
	raw := strings.TrimSpace(l.getenv(key))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < minimum || n > maximum {
		l.fail("invalid %s %q: must be an integer between %d and %d", key, raw, minimum, maximum)
		return def
	}
	return n
}

// lenientInt keeps the historical behavior: an invalid value falls back to
// the default with a warning instead of aborting startup.
func (l *loader) lenientInt(key string, def int) int {
	raw := strings.TrimSpace(l.getenv(key))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		l.warn("invalid %s %q, using %d", key, raw, def)
		return def
	}
	return n
}

func (l *loader) boolean(key string, def bool) bool {
	raw := l.getenv(key)
	if raw == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		l.warn("invalid %s %q, using %t", key, raw, def)
		return def
	}
}

func (l *loader) location(key string) *time.Location {
	raw := strings.TrimSpace(l.getenv(key))
	if raw == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(raw)
	if err != nil {
		l.warn("invalid %s %q (%v), using UTC", key, raw, err)
		return time.UTC
	}
	return loc
}

func (l *loader) logLevel(key string) slog.Level {
	raw := strings.TrimSpace(l.getenv(key))
	var lvl slog.Level
	if raw == "" {
		return lvl
	}
	if err := lvl.UnmarshalText([]byte(raw)); err != nil {
		l.fail("invalid %s %q: want debug, info, warn or error", key, raw)
	}
	return lvl
}

func (l *loader) logFormat(key string) string {
	switch raw := strings.ToLower(strings.TrimSpace(l.getenv(key))); raw {
	case "", "json":
		return "json"
	case "text":
		return "text"
	default:
		l.fail("invalid %s %q: want json or text", key, raw)
		return "json"
	}
}

// ParseList splits a comma-separated value, trimming blanks.
func ParseList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// ParseCORSOrigins parses CORS_ORIGINS. It deduplicates, drops "*" and falls
// back to DefaultCORSOrigins when nothing usable remains.
func ParseCORSOrigins(raw string) []string {
	seen := make(map[string]bool)
	var origins []string
	for _, origin := range ParseList(raw) {
		if origin == "*" || seen[origin] {
			continue
		}
		seen[origin] = true
		origins = append(origins, origin)
	}
	if len(origins) == 0 {
		return append([]string(nil), DefaultCORSOrigins...)
	}
	return origins
}
