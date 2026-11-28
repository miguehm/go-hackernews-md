package todo

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizeDueDate(t *testing.T) {
	cases := []struct {
		name  string
		input *string
		want  *string
		err   bool
	}{
		{name: "nil is no deadline"},
		{name: "empty is no deadline", input: strPtr("")},
		{name: "blank is no deadline", input: strPtr("   ")},
		{name: "valid", input: strPtr("2026-11-30"), want: strPtr("2026-11-30")},
		{name: "trimmed", input: strPtr(" 2026-11-30 "), want: strPtr("2026-11-30")},
		{name: "wrong order", input: strPtr("30-11-2026"), err: true},
		{name: "impossible day", input: strPtr("2026-02-30"), err: true},
		{name: "timestamp rejected", input: strPtr("2026-11-30T00:00:00Z"), err: true},
		{name: "garbage", input: strPtr("manana"), err: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeDueDate(tc.input)
			if (err != nil) != tc.err {
				t.Fatalf("error = %v, want err %v", err, tc.err)
			}
			if tc.err {
				var ve *ValidationError
				if !errors.As(err, &ve) || ve.Field != "due_date" {
					t.Fatalf("error = %#v, want ValidationError{due_date}", err)
				}
				return
			}
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCloneTaskDueDateIsolation(t *testing.T) {
	original := Task{ID: "1", DueDate: strPtr("2026-11-30")}
	clone := CloneTask(original)
	*clone.DueDate = "2027-01-01"
	if *original.DueDate != "2026-11-30" {
		t.Fatalf("CloneTask aliased due_date: %q", *original.DueDate)
	}
}

func TestComputeCountsWithDue(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	tasks := []Task{
		{ID: "overdue", DueDate: strPtr("2026-11-20")},
		{ID: "today", DueDate: strPtr("2026-11-23")},
		{ID: "soon", DueDate: strPtr("2026-11-26")},
		{ID: "later", DueDate: strPtr("2026-11-27")},
		{ID: "none"},
		{ID: "done-overdue", DueDate: strPtr("2026-11-01"), Done: true},
	}

	counts := ComputeCountsWith(tasks, CountOptions{Now: now})
	if counts.Open != 5 || counts.Done != 1 || counts.Total != 6 {
		t.Fatalf("base counts = %+v", counts)
	}
	if counts.Overdue != 1 || counts.DueSoon != 2 {
		t.Fatalf("due counts = %+v, want overdue 1, due_soon 2", counts)
	}

	wide := ComputeCountsWith(tasks, CountOptions{Now: now, DueDays: 7})
	if wide.DueSoon != 3 || wide.Overdue != 1 {
		t.Fatalf("wide due counts = %+v", wide)
	}
}

func TestComputeTagCountsWithDue(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	tasks := []Task{
		{ID: "1", Tags: []string{"casa"}, DueDate: strPtr("2026-11-20")},
		{ID: "2", Tags: []string{"casa"}, DueDate: strPtr("2026-11-25")},
		{ID: "3", Tags: []string{"casa"}, Done: true, DueDate: strPtr("2026-11-01")},
		{ID: "4", DueDate: strPtr("2026-11-26")},
	}
	tags, _, untagged := ComputeTagCountsWith(tasks, CountOptions{Now: now})
	if len(tags) != 1 || tags[0].Tag != "casa" {
		t.Fatalf("tags = %+v", tags)
	}
	if tags[0].Overdue != 1 || tags[0].DueSoon != 1 || tags[0].Open != 2 || tags[0].Done != 1 {
		t.Fatalf("casa counts = %+v", tags[0])
	}
	if untagged.DueSoon != 1 || untagged.Overdue != 0 || untagged.Open != 1 {
		t.Fatalf("untagged counts = %+v", untagged)
	}
}

func TestFilterTasksDueStatuses(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	tasks := []Task{
		{ID: "overdue", Tags: []string{"casa"}, DueDate: strPtr("2026-11-20")},
		{ID: "today", Tags: []string{"trabajo"}, DueDate: strPtr("2026-11-23")},
		{ID: "soon", Tags: []string{"casa"}, DueDate: strPtr("2026-11-26")},
		{ID: "later", Tags: []string{"trabajo"}, DueDate: strPtr("2026-11-30")},
		{ID: "none"},
		{ID: "done-overdue", Done: true, DueDate: strPtr("2026-11-01")},
	}
	cases := []struct {
		filter Filter
		want   string
	}{
		{Filter{Status: "overdue"}, "overdue"},
		{Filter{Status: "due_soon"}, "today,soon"},
		{Filter{Status: "due_soon", DueDays: 7}, "today,soon,later"},
		{Filter{Status: "due_soon", Tag: "casa"}, "soon"},
		{Filter{Status: "all"}, "overdue,today,soon,later,none,done-overdue"},
	}
	for _, tc := range cases {
		got := FilterTasksAt(tasks, tc.filter, now)
		ids := make([]string, 0, len(got))
		for _, task := range got {
			ids = append(ids, task.ID)
		}
		if joined := strings.Join(ids, ","); joined != tc.want {
			t.Fatalf("FilterTasksAt(%+v) = %s, want %s", tc.filter, joined, tc.want)
		}
	}
}

func TestSortTasksDueFirst(t *testing.T) {
	tasks := []Task{
		{ID: "no-date-b", Tags: []string{"casa"}, CreatedAt: "2026-11-01T00:00:00Z"},
		{ID: "late", DueDate: strPtr("2026-12-01"), Tags: []string{"z"}, CreatedAt: "2026-11-02T00:00:00Z"},
		{ID: "no-date-a", Tags: []string{"casa"}, CreatedAt: "2026-11-01T00:00:00Z"},
		{ID: "soon", DueDate: strPtr("2026-11-25"), Tags: []string{"a"}, CreatedAt: "2026-11-03T00:00:00Z"},
		{ID: "overdue", DueDate: strPtr("2026-11-20"), Tags: []string{"z"}, CreatedAt: "2026-11-04T00:00:00Z"},
		{ID: "done", Done: true, DoneAt: strPtr("2026-11-22T00:00:00Z")},
	}
	ids := make([]string, 0, len(tasks))
	for _, task := range SortTasks(tasks) {
		ids = append(ids, task.ID)
	}
	want := "overdue,soon,late,no-date-a,no-date-b,done"
	if joined := strings.Join(ids, ","); joined != want {
		t.Fatalf("sorted = %s, want %s", joined, want)
	}
}

func TestRenderMarkdownDueDate(t *testing.T) {
	tasks := []Task{{ID: "1", Title: "Pagar", Tags: []string{"casa"}, DueDate: strPtr("2026-11-30")}}
	md := RenderMarkdown(tasks, RenderOptions{Filter: Filter{Status: "all"}, Counts: ComputeCounts(tasks), GeneratedAt: "2026-11-23 12:00"})
	if !strings.Contains(md, "- [ ] Pagar (vence 2026-11-30)") {
		t.Fatalf("deadline missing from markdown:\n%s", md)
	}
}

func TestStoreDueDateBackwardCompatibility(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "todos.json")
	raw := `{"schema_version":1,"updated_at":"2026-11-22T10:00:00Z","tasks":[{"id":"old1","title":"Old","tags":[],"done":false,"created_at":"2026-11-22T10:00:00Z","updated_at":"2026-11-22T10:00:00Z","revision":1}]}`
	if err := os.WriteFile(file, []byte(raw), 0o600); err != nil {
		t.Fatalf("write store: %v", err)
	}
	st, err := NewStore(file, 10)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	old, _ := st.Get("old1")
	if old.DueDate != nil {
		t.Fatalf("legacy due_date = %q, want nil", *old.DueDate)
	}

	created, err := st.Create(Task{ID: "new1", Title: "New", Tags: []string{}, DueDate: strPtr("2026-11-30"), CreatedAt: "2026-11-22T11:00:00Z", UpdatedAt: "2026-11-22T11:00:00Z", Revision: 1})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.DueDate == nil || *created.DueDate != "2026-11-30" {
		t.Fatalf("created due_date = %v", created.DueDate)
	}
	reloaded, err := NewStore(file, 10)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, _ := reloaded.Get("new1")
	if got.DueDate == nil || *got.DueDate != "2026-11-30" {
		t.Fatalf("reloaded due_date = %v", got.DueDate)
	}
}

func TestStoreRejectsInvalidDueDate(t *testing.T) {
	file := filepath.Join(t.TempDir(), "todos.json")
	raw := `{"schema_version":1,"updated_at":"2026-11-22T10:00:00Z","tasks":[{"id":"bad","title":"Bad","tags":[],"due_date":"30-11-2026","done":false,"created_at":"2026-11-22T10:00:00Z","updated_at":"2026-11-22T10:00:00Z","revision":1}]}`
	if err := os.WriteFile(file, []byte(raw), 0o600); err != nil {
		t.Fatalf("write store: %v", err)
	}
	if _, err := NewStore(file, 10); err == nil || !strings.Contains(err.Error(), "invalid due_date") {
		t.Fatalf("error = %v, want invalid due_date", err)
	}
}
