package todo

import (
	"regexp"
	"strings"
	"testing"
)

var taskIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func TestParseText(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		extra    []string
		title    string
		tags     []string
		wantErr  bool
		errField string
	}{
		{
			name:  "multiple tags",
			text:  "Comprar leche #casa #urgente",
			title: "Comprar leche",
			tags:  []string{"casa", "urgente"},
		},
		{
			name:  "uppercase tag at start",
			text:  "#Casa Comprar pan",
			title: "Comprar pan",
			tags:  []string{"casa"},
		},
		{
			name:  "duplicates and explicit tags",
			text:  "Tarea #CASA #casa",
			extra: []string{"Trabajo", "casa"},
			title: "Tarea",
			tags:  []string{"casa", "trabajo"},
		},
		{
			name:  "hash in word",
			text:  "C# tutorial",
			title: "C# tutorial",
			tags:  []string{},
		},
		{
			name:  "url fragment",
			text:  "Ver https://example.com/#intro",
			title: "Ver https://example.com/#intro",
			tags:  []string{},
		},
		{
			name:  "unicode tag is not parsed",
			text:  "Comprar #café",
			title: "Comprar #café",
			tags:  []string{},
		},
		{
			name:     "empty text",
			text:     "   ",
			wantErr:  true,
			errField: "text",
		},
		{
			name:     "title required",
			text:     "#solo",
			wantErr:  true,
			errField: "text",
		},
		{
			name:     "too many tags",
			text:     "t #a #b #c #d #e #f #g #h #i #j #k",
			wantErr:  true,
			errField: "tags",
		},
		{
			name:     "invalid explicit tag",
			text:     "tarea",
			extra:    []string{"bad tag"},
			wantErr:  true,
			errField: "tags",
		},
		{
			name:     "text too long",
			text:     strings.Repeat("x", MaxTextBytes+1),
			wantErr:  true,
			errField: "text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, tags, err := ParseText(tt.text, tt.extra)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				var ve *ValidationError
				if !asValidationError(err, &ve) {
					t.Fatalf("error type = %T, want *ValidationError", err)
				}
				if ve.Field != tt.errField {
					t.Fatalf("error field = %q, want %q", ve.Field, tt.errField)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if title != tt.title {
				t.Fatalf("title = %q, want %q", title, tt.title)
			}
			if strings.Join(tags, ",") != strings.Join(tt.tags, ",") {
				t.Fatalf("tags = %v, want %v", tags, tt.tags)
			}
		})
	}
}

func asValidationError(err error, target **ValidationError) bool {
	if ve, ok := err.(*ValidationError); ok {
		*target = ve
		return true
	}
	return false
}

func TestNewID(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id, err := NewID()
		if err != nil {
			t.Fatalf("NewID: %v", err)
		}
		if !taskIDPattern.MatchString(id) {
			t.Fatalf("invalid id %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestNormalizeTags(t *testing.T) {
	tags, err := NormalizeTags([]string{"CASA", "casa", "trabajo"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(tags, ",") != "casa,trabajo" {
		t.Fatalf("tags = %v", tags)
	}
	if _, err := NormalizeTags([]string{"-bad"}); err == nil {
		t.Fatal("expected error for invalid tag")
	}
	if _, err := NormalizeTags([]string{strings.Repeat("a", MaxTagLength+1)}); err == nil {
		t.Fatal("expected error for too long tag")
	}
}

func TestComputeTagCounts(t *testing.T) {
	tasks := []Task{
		{ID: "1", Tags: []string{"casa", "urgente"}},
		{ID: "2", Tags: []string{"casa"}, Done: true},
		{ID: "3"},
	}
	tags, counts, untagged := ComputeTagCounts(tasks)
	if counts.Open != 2 || counts.Done != 1 || counts.Total != 3 {
		t.Fatalf("counts = %+v", counts)
	}
	if untagged.Open != 1 || untagged.Done != 0 || untagged.Total != 1 {
		t.Fatalf("untagged = %+v", untagged)
	}
	if len(tags) != 2 || tags[0].Tag != "casa" || tags[0].Open != 1 || tags[0].Done != 1 || tags[0].Total != 2 {
		t.Fatalf("tags = %+v", tags)
	}
	if tags[1].Tag != "urgente" || tags[1].Total != 1 {
		t.Fatalf("tags = %+v", tags)
	}
}
