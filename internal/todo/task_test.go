package todo

import (
	"errors"
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

func TestValidationErrorError(t *testing.T) {
	err := validationError("text", "text is required")
	if err.Error() != "text is required" {
		t.Fatalf("Error() = %q, want %q", err.Error(), "text is required")
	}
	ve, ok := err.(*ValidationError)
	if !ok || ve.Field != "text" {
		t.Fatalf("error = %+v, want *ValidationError with field text", err)
	}
}

func TestParseTextEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		title    string
		tags     []string
		errField string
		message  string
	}{
		{
			name:  "title at limit",
			text:  strings.Repeat("a", MaxTitleSize),
			title: strings.Repeat("a", MaxTitleSize),
			tags:  []string{},
		},
		{
			name:     "title too long",
			text:     strings.Repeat("a", MaxTitleSize+1),
			errField: "text",
			message:  "title must be at most 200 characters",
		},
		{
			name:  "hashtag longer than limit is not parsed",
			text:  "Tarea #" + strings.Repeat("a", MaxTagLength+8),
			title: "Tarea #" + strings.Repeat("a", MaxTagLength+8),
			tags:  []string{},
		},
		{
			name:  "punctuation boundary",
			text:  "Listo #casa.",
			title: "Listo",
			tags:  []string{"casa"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, tags, err := ParseText(tt.text, nil)
			if tt.errField != "" {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				var ve *ValidationError
				if !asValidationError(err, &ve) || ve.Field != tt.errField || ve.Message != tt.message {
					t.Fatalf("error = %+v, want field %q and message %q", err, tt.errField, tt.message)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if title != tt.title || strings.Join(tags, ",") != strings.Join(tt.tags, ",") {
				t.Fatalf("ParseText = %q, %v; want %q, %v", title, tags, tt.title, tt.tags)
			}
		})
	}
}

func TestFilterNormalized(t *testing.T) {
	tests := []struct {
		filter Filter
		want   Filter
	}{
		{filter: Filter{Tag: "  CASA ", Status: "weird"}, want: Filter{Tag: "casa", Status: "open", DueDays: DefaultDueDays}},
		{filter: Filter{}, want: Filter{Status: "open", DueDays: DefaultDueDays}},
		{filter: Filter{Status: "done"}, want: Filter{Status: "done", DueDays: DefaultDueDays}},
		{filter: Filter{Status: "all"}, want: Filter{Status: "all", DueDays: DefaultDueDays}},
		{filter: Filter{Status: "due_soon", DueDays: 7}, want: Filter{Status: "due_soon", DueDays: 7}},
		{filter: Filter{Status: "overdue"}, want: Filter{Status: "overdue", DueDays: DefaultDueDays}},
	}
	for _, tt := range tests {
		if got := tt.filter.Normalized(); got != tt.want {
			t.Fatalf("Normalized(%+v) = %+v, want %+v", tt.filter, got, tt.want)
		}
	}
}

func TestNormalizeTagsSkipsEmptyEntries(t *testing.T) {
	tags, err := NormalizeTags([]string{"", "  ", "casa"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(tags, ",") != "casa" {
		t.Fatalf("tags = %v, want [casa]", tags)
	}
}

func TestCloneTaskDoesNotAliasDoneAt(t *testing.T) {
	doneAt := "2026-11-22T10:00:00Z"
	original := Task{ID: "1", Tags: []string{"casa"}, DoneAt: &doneAt}
	cloned := CloneTask(original)
	if cloned.ID != original.ID || strings.Join(cloned.Tags, ",") != "casa" || cloned.DoneAt == nil || *cloned.DoneAt != doneAt {
		t.Fatalf("cloned = %+v", cloned)
	}
	cloned.Tags[0] = "otro"
	*cloned.DoneAt = "2026-11-23T10:00:00Z"
	if original.Tags[0] != "casa" || *original.DoneAt != doneAt {
		t.Fatalf("CloneTask aliased original: %+v", original)
	}

	empty := CloneTasks(nil)
	if empty == nil || len(empty) != 0 {
		t.Fatalf("CloneTasks(nil) = %v, want empty non-nil slice", empty)
	}
}

func TestComputeTagCountsUntaggedDone(t *testing.T) {
	_, _, untagged := ComputeTagCounts([]Task{{ID: "1", Done: true}, {ID: "2"}})
	if untagged.Done != 1 || untagged.Open != 1 || untagged.Total != 2 {
		t.Fatalf("untagged = %+v, want 1 done, 1 open, 2 total", untagged)
	}
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

func TestNormalizeAndValidateDetails(t *testing.T) {
	got := NormalizeDetails("primera\r\nsegunda\r\n   ")
	if got != "primera\nsegunda" {
		t.Fatalf("NormalizeDetails = %q", got)
	}
	if err := ValidateDetails(strings.Repeat("x", MaxDetailsBytes)); err != nil {
		t.Fatalf("expected details at the limit to be valid: %v", err)
	}
	err := ValidateDetails(strings.Repeat("x", MaxDetailsBytes+1))
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || validationErr.Field != "details" {
		t.Fatalf("expected field=details error, got %v", err)
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
