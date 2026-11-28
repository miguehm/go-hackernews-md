package config

import (
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func envFrom(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(envFrom(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Config{
		Port:            "8982",
		RefreshInterval: 30 * time.Minute,
		ScrapeTimeout:   10 * time.Second,
		HNTimeout:       10 * time.Second,
		TopN:            20,
		MaxConcurrency:  6,
		MaxBodySize:     5 * 1024 * 1024,
		CORSOrigins:     DefaultCORSOrigins,
		DataDir:         "./data",
		TodoFile:        filepath.Join("./data", "todos.json"),
		TodoMaxTasks:    1000,
		TodoLocation:    time.UTC,
		LogLevel:        slog.LevelInfo,
		LogFormat:       "json",
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("cfg = %+v\nwant %+v", cfg, want)
	}
	if cfg.AuthEnabled() {
		t.Fatal("auth must be disabled without API_KEYS")
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := Load(envFrom(map[string]string{
		"PORT":                       "9000",
		"REFRESH_INTERVAL":           "5m",
		"SCRAPE_TIMEOUT":             "3s",
		"HN_TIMEOUT":                 "2s",
		"TOP_N":                      "30",
		"MAX_CONCURRENCY":            "12",
		"API_KEYS":                   " k1 , ,k2 ",
		"PROTECT_HEALTH":             "yes",
		"CORS_ORIGINS":               "https://app.example",
		"DATA_DIR":                   "/data",
		"TODO_MAX_TASKS":             "50",
		"TODO_ALLOW_INSECURE_WRITES": "on",
		"TODO_TIMEZONE":              "America/Mexico_City",
		"PUBLIC_BASE_URL":            "https://hnmd.example",
		"LOG_LEVEL":                  "debug",
		"LOG_FORMAT":                 "TEXT",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != "9000" || cfg.RefreshInterval != 5*time.Minute || cfg.ScrapeTimeout != 3*time.Second || cfg.HNTimeout != 2*time.Second {
		t.Fatalf("basic fields = %+v", cfg)
	}
	if cfg.TopN != 30 || cfg.MaxConcurrency != 12 || !cfg.ProtectHealth || !cfg.TodoAllowInsecureWrites || cfg.TodoMaxTasks != 50 {
		t.Fatalf("numeric/bool fields = %+v", cfg)
	}
	if !reflect.DeepEqual(cfg.APIKeys, []string{"k1", "k2"}) || !cfg.AuthEnabled() {
		t.Fatalf("APIKeys = %v", cfg.APIKeys)
	}
	if cfg.TodoFile != filepath.Join("/data", "todos.json") || cfg.TodoLocation.String() != "America/Mexico_City" {
		t.Fatalf("todo fields = %q, %v", cfg.TodoFile, cfg.TodoLocation)
	}
	if cfg.LogLevel != slog.LevelDebug || cfg.LogFormat != "text" || len(cfg.Warnings) != 0 {
		t.Fatalf("log fields = %v %q, warnings %v", cfg.LogLevel, cfg.LogFormat, cfg.Warnings)
	}
}

func TestLoadJoinsAllErrors(t *testing.T) {
	_, err := Load(envFrom(map[string]string{
		"REFRESH_INTERVAL": "0s",
		"SCRAPE_TIMEOUT":   "abc",
		"HN_TIMEOUT":       "-1s",
		"TOP_N":            "0",
		"MAX_CONCURRENCY":  "1000",
		"LOG_LEVEL":        "loud",
		"LOG_FORMAT":       "xml",
	}))
	if err == nil {
		t.Fatal("want error")
	}
	for _, key := range []string{"REFRESH_INTERVAL", "SCRAPE_TIMEOUT", "HN_TIMEOUT", "TOP_N", "MAX_CONCURRENCY", "LOG_LEVEL", "LOG_FORMAT"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error does not mention %s:\n%v", key, err)
		}
	}
}

func TestLoadLenientValuesWarn(t *testing.T) {
	cfg, err := Load(envFrom(map[string]string{
		"TODO_MAX_TASKS": "lots",
		"PROTECT_HEALTH": "maybe",
		"TODO_TIMEZONE":  "Mars/Base",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TodoMaxTasks != 1000 || cfg.ProtectHealth || cfg.TodoLocation != time.UTC {
		t.Fatalf("lenient defaults = %+v", cfg)
	}
	if len(cfg.Warnings) != 3 {
		t.Fatalf("warnings = %v, want 3", cfg.Warnings)
	}
}

func TestBooleanValues(t *testing.T) {
	for raw, want := range map[string]bool{"1": true, " TrUe ": true, "yes": true, "ON": true, "0": false, " false ": false, "No": false, "OFF": false} {
		cfg, err := Load(envFrom(map[string]string{"PROTECT_HEALTH": raw}))
		if err != nil || cfg.ProtectHealth != want || len(cfg.Warnings) != 0 {
			t.Errorf("PROTECT_HEALTH=%q: got %v (warnings %v, err %v), want %v", raw, cfg.ProtectHealth, cfg.Warnings, err, want)
		}
	}
}

func TestParseCORSOrigins(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "unset uses defaults", raw: "", want: DefaultCORSOrigins},
		{name: "whitespace uses defaults", raw: "  ", want: DefaultCORSOrigins},
		{name: "wildcard only uses defaults", raw: "*", want: DefaultCORSOrigins},
		{name: "single", raw: "https://app.example", want: []string{"https://app.example"}},
		{name: "override with spaces, blanks and duplicates", raw: " web , , web , http://localhost:5173 ", want: []string{"web", "http://localhost:5173"}},
		{name: "wildcard is skipped", raw: " *, https://app.example ", want: []string{"https://app.example"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseCORSOrigins(tt.raw); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseCORSOrigins(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
	got := ParseCORSOrigins("")
	got[0] = "mutated"
	if DefaultCORSOrigins[0] == "mutated" {
		t.Fatal("defaults must be copied")
	}
}

func TestParseList(t *testing.T) {
	if got := ParseList("   "); got != nil {
		t.Fatalf("ParseList(blank) = %v", got)
	}
	if got := ParseList(" a , ,b ,c "); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("ParseList = %v", got)
	}
}
