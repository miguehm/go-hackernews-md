package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"

	"hnmd/internal/api"
)

func TestGetenv(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		t.Setenv("HNMD_TEST_KEY", "value")
		if got := getenv("HNMD_TEST_KEY", "default"); got != "value" {
			t.Fatalf("getenv = %q, want value", got)
		}
	})
	t.Run("unset uses default", func(t *testing.T) {
		t.Setenv("HNMD_TEST_KEY", "")
		if got := getenv("HNMD_TEST_KEY", "default"); got != "default" {
			t.Fatalf("getenv = %q, want default", got)
		}
	})
}

func TestParseBoolEnv(t *testing.T) {
	tests := []struct {
		name  string
		value string
		def   bool
		want  bool
	}{
		{name: "unset", def: true, want: true},
		{name: "empty", value: "", def: true, want: true},
		{name: "one", value: "1", want: true},
		{name: "true mixed case", value: " TrUe ", want: true},
		{name: "yes", value: "yes", want: true},
		{name: "on", value: "ON", want: true},
		{name: "zero", value: "0", def: true, want: false},
		{name: "false", value: " false ", def: true, want: false},
		{name: "no", value: "No", def: true, want: false},
		{name: "off", value: "OFF", def: true, want: false},
		{name: "invalid uses default true", value: "maybe", def: true, want: true},
		{name: "invalid uses default false", value: "maybe", def: false, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.value == "" {
				t.Setenv("HNMD_TEST_BOOL", "")
			} else {
				t.Setenv("HNMD_TEST_BOOL", tt.value)
			}
			if got := parseBoolEnv("HNMD_TEST_BOOL", tt.def); got != tt.want {
				t.Fatalf("parseBoolEnv(%q, %v) = %v, want %v", tt.value, tt.def, got, tt.want)
			}
		})
	}
}

func TestParseIntEnv(t *testing.T) {
	tests := []struct {
		name  string
		value string
		def   int
		want  int
	}{
		{name: "unset", def: 1000, want: 1000},
		{name: "empty", value: "", def: 1000, want: 1000},
		{name: "valid", value: "42", def: 1000, want: 42},
		{name: "valid with spaces", value: " 17 ", def: 1000, want: 17},
		{name: "negative", value: "-5", def: 1000, want: -5},
		{name: "invalid", value: "abc", def: 1000, want: 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HNMD_TEST_INT", tt.value)
			if got := parseIntEnv("HNMD_TEST_INT", tt.def); got != tt.want {
				t.Fatalf("parseIntEnv(%q, %d) = %d, want %d", tt.value, tt.def, got, tt.want)
			}
		})
	}
}

func TestParseCORSOrigins(t *testing.T) {
	defaults := append([]string(nil), api.DefaultCORSOrigins...)
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "unset uses defaults", raw: "", want: defaults},
		{name: "whitespace uses defaults", raw: "  ", want: defaults},
		{name: "wildcard only uses defaults", raw: "*", want: defaults},
		{name: "single", raw: "https://app.example", want: []string{"https://app.example"}},
		{name: "override with spaces, blanks and duplicates", raw: " web , , web , http://localhost:5173 ", want: []string{"web", "http://localhost:5173"}},
		{name: "wildcard is skipped", raw: " *, https://app.example ", want: []string{"https://app.example"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCORSOrigins(tt.raw)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseCORSOrigins(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseAPIKeys(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "empty", raw: "", want: nil},
		{name: "whitespace", raw: "   ", want: nil},
		{name: "single", raw: "key1", want: []string{"key1"}},
		{name: "multiple with spaces and blanks", raw: " key1 , , key2 ,key3 ", want: []string{"key1", "key2", "key3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseAPIKeys(tt.raw)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseAPIKeys(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseDurationEnv(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    time.Duration
		wantErr bool
	}{
		{name: "default", value: "", want: 10 * time.Second},
		{name: "valid", value: "250ms", want: 250 * time.Millisecond},
		{name: "invalid", value: "abc", wantErr: true},
		{name: "zero", value: "0s", wantErr: true},
		{name: "negative", value: "-1m", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HNMD_TEST_DURATION", tt.value)
			got, err := parseDurationEnv("HNMD_TEST_DURATION", "10s")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestNewLogger(t *testing.T) {
	var buf bytes.Buffer
	logger, err := newLogger(&buf, "warn", "")
	if err != nil {
		t.Fatalf("newLogger: %v", err)
	}
	logger.Info("hidden")
	logger.Warn("shown", "k", "v")
	out := buf.String()
	if strings.Contains(out, "hidden") || !strings.Contains(out, `"msg":"shown"`) || !strings.Contains(out, `"k":"v"`) {
		t.Fatalf("json/warn output = %q", out)
	}

	buf.Reset()
	logger, err = newLogger(&buf, "", "text")
	if err != nil {
		t.Fatalf("newLogger text: %v", err)
	}
	logger.Info("hello")
	if !strings.Contains(buf.String(), "msg=hello") {
		t.Fatalf("text output = %q", buf.String())
	}

	if _, err := newLogger(&buf, "loud", ""); err == nil {
		t.Fatal("want error for invalid LOG_LEVEL")
	}
	if _, err := newLogger(&buf, "", "xml"); err == nil {
		t.Fatal("want error for invalid LOG_FORMAT")
	}
}
