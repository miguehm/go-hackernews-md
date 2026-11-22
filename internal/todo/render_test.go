package todo

import (
	"strings"
	"testing"
)

func strPtr(v string) *string { return &v }

func renderFixtures() []Task {
	return []Task{
		{ID: "1", Title: "Comprar leche", Tags: []string{"casa", "urgente"}, CreatedAt: "2026-11-22T10:00:00Z", UpdatedAt: "2026-11-22T10:00:00Z", Revision: 1},
		{ID: "2", Title: "Pagar alquiler", Tags: []string{"casa"}, CreatedAt: "2026-11-22T11:00:00Z", UpdatedAt: "2026-11-22T11:00:00Z", Revision: 1},
		{ID: "3", Title: "Enviar informe", Tags: []string{"trabajo"}, CreatedAt: "2026-11-22T12:00:00Z", UpdatedAt: "2026-11-22T12:00:00Z", Revision: 1},
		{ID: "4", Title: "Llamar al médico", Tags: []string{}, CreatedAt: "2026-11-22T13:00:00Z", UpdatedAt: "2026-11-22T13:00:00Z", Revision: 1},
		{ID: "5", Title: "Revisar correo", Tags: []string{"trabajo"}, Done: true, DoneAt: strPtr("2026-11-22T14:00:00Z"), CreatedAt: "2026-11-22T09:00:00Z", UpdatedAt: "2026-11-22T14:00:00Z", Revision: 2},
	}
}

func TestRenderMarkdownGroups(t *testing.T) {
	tasks := renderFixtures()
	md := RenderMarkdown(tasks, RenderOptions{
		Filter:      Filter{Status: "all"},
		Counts:      ComputeCounts(tasks),
		GeneratedAt: "2026-11-22 15:00",
	})

	want := `# Todos

> 4 abiertas · 1 hechas · 2026-11-22 15:00

## #casa
- [ ] Comprar leche #urgente
- [ ] Pagar alquiler

## #trabajo
- [ ] Enviar informe

## Sin etiqueta
- [ ] Llamar al médico

## Hechas
- [x] Revisar correo
`
	if md != want {
		t.Fatalf("markdown =\n%s\nwant\n%s", md, want)
	}
}

func TestRenderMarkdownOpenOnly(t *testing.T) {
	tasks := renderFixtures()
	md := RenderMarkdown(tasks, RenderOptions{
		Filter:      Filter{Status: "open"},
		Counts:      ComputeCounts(tasks),
		GeneratedAt: "2026-11-22 15:00",
	})
	if strings.Contains(md, "## Hechas") || strings.Contains(md, "Revisar correo") {
		t.Fatalf("done tasks should not be rendered: %s", md)
	}
	if !strings.Contains(md, "## #casa") {
		t.Fatalf("missing tag group: %s", md)
	}
}

func TestRenderMarkdownTagViewIsFlat(t *testing.T) {
	tasks := renderFixtures()
	md := RenderMarkdown(tasks, RenderOptions{
		Filter:      Filter{Tag: "casa", Status: "all"},
		Counts:      ComputeCounts(tasks),
		GeneratedAt: "2026-11-22 15:00",
	})
	if !strings.HasPrefix(md, "# Todos — #casa\n") {
		t.Fatalf("unexpected heading: %s", md)
	}
	if strings.Contains(md, "## #casa") {
		t.Fatalf("tag view should be flat: %s", md)
	}
	if !strings.Contains(md, "- [ ] Comprar leche #urgente") {
		t.Fatalf("missing task line: %s", md)
	}
	if !strings.Contains(md, "- [ ] Pagar alquiler\n") {
		t.Fatalf("missing task line: %s", md)
	}
}

func TestRenderMarkdownEmpty(t *testing.T) {
	md := RenderMarkdown(nil, RenderOptions{Counts: Counts{}, GeneratedAt: "2026-11-22 15:00"})
	if !strings.Contains(md, "_No hay tareas._") {
		t.Fatalf("empty markdown = %s", md)
	}
}

func TestRenderMarkdownTruncated(t *testing.T) {
	md := RenderMarkdown(nil, RenderOptions{Counts: Counts{}, GeneratedAt: "2026-11-22 15:00", Truncated: true})
	if !strings.Contains(md, "> Lista truncada.") {
		t.Fatalf("truncated markdown = %s", md)
	}
}

func TestEscapeMarkdown(t *testing.T) {
	got := escapeMarkdown(`*negrita* [link] #tag <b>`)
	if got != `\*negrita\* \[link\] \#tag \<b\>` {
		t.Fatalf("escape = %q", got)
	}
	got = escapeMarkdown("  back\\slash `code` _ undersc  ore  ")
	if got != "back\\\\slash \\`code\\` \\_ undersc ore" {
		t.Fatalf("escape = %q", got)
	}
}

func TestSortTasksDoneOrder(t *testing.T) {
	tasks := []Task{
		{ID: "1", Done: true, DoneAt: strPtr("2026-11-22T10:00:00Z")},
		{ID: "2", Done: true, DoneAt: strPtr("2026-11-22T12:00:00Z")},
	}
	sorted := SortTasks(tasks)
	if sorted[0].ID != "2" || sorted[1].ID != "1" {
		t.Fatalf("sorted = %+v", sorted)
	}
}

func TestFilterTasks(t *testing.T) {
	tasks := []Task{
		{ID: "1", Tags: []string{"casa"}, CreatedAt: "2026-11-22T10:00:00Z"},
		{ID: "2", Tags: []string{"casa"}, Done: true, DoneAt: strPtr("2026-11-22T11:00:00Z")},
		{ID: "3", Tags: []string{"trabajo"}, CreatedAt: "2026-11-22T09:00:00Z"},
	}
	tests := []struct {
		name   string
		filter Filter
		want   string
	}{
		{name: "tag with normalization and open default", filter: Filter{Tag: "  CASA "}, want: "1"},
		{name: "tag all statuses", filter: Filter{Tag: "casa", Status: "all"}, want: "1,2"},
		{name: "done only", filter: Filter{Status: "done"}, want: "2"},
		{name: "all sorted", filter: Filter{Status: "all"}, want: "1,3,2"},
		{name: "default open", filter: Filter{}, want: "1,3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterTasks(tasks, tt.filter)
			ids := make([]string, 0, len(got))
			for _, task := range got {
				ids = append(ids, task.ID)
			}
			if strings.Join(ids, ",") != tt.want {
				t.Fatalf("ids = %v, want %s", ids, tt.want)
			}
		})
	}

	got := FilterTasks(tasks, Filter{Tag: "casa"})
	got[0].Tags[0] = "mutated"
	if tasks[0].Tags[0] != "casa" {
		t.Fatalf("FilterTasks returned an aliased task: %+v", tasks[0])
	}
}

func TestContainsTag(t *testing.T) {
	if !containsTag([]string{"casa", "urgente"}, "urgente") {
		t.Fatal("expected match")
	}
	if containsTag([]string{"casa"}, "trabajo") || containsTag(nil, "casa") {
		t.Fatal("expected no match")
	}
}

func TestSortTasksTieBreaks(t *testing.T) {
	tests := []struct {
		name  string
		tasks []Task
		want  string
	}{
		{
			name: "tagged before untagged regardless of creation date",
			tasks: []Task{
				{ID: "u", CreatedAt: "2026-01-01T00:00:00Z"},
				{ID: "t", Tags: []string{"a"}, CreatedAt: "2026-12-01T00:00:00Z"},
			},
			want: "t,u",
		},
		{
			name: "open tasks with same tag and date tie-break by id",
			tasks: []Task{
				{ID: "b", Tags: []string{"a"}, CreatedAt: "same"},
				{ID: "a", Tags: []string{"a"}, CreatedAt: "same"},
			},
			want: "a,b",
		},
		{
			name: "done with timestamp before done without",
			tasks: []Task{
				{ID: "nil", Done: true},
				{ID: "set", Done: true, DoneAt: strPtr("2026-11-22T10:00:00Z")},
			},
			want: "set,nil",
		},
		{
			name: "done tasks with same timestamp tie-break by id",
			tasks: []Task{
				{ID: "b", Done: true, DoneAt: strPtr("same")},
				{ID: "a", Done: true, DoneAt: strPtr("same")},
			},
			want: "a,b",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := make([]string, 0, len(tt.tasks))
			for _, task := range tt.tasks {
				original = append(original, task.ID)
			}
			sorted := SortTasks(tt.tasks)
			ids := make([]string, 0, len(sorted))
			for _, task := range sorted {
				ids = append(ids, task.ID)
			}
			if strings.Join(ids, ",") != tt.want {
				t.Fatalf("ids = %v, want %s", ids, tt.want)
			}
			after := make([]string, 0, len(tt.tasks))
			for _, task := range tt.tasks {
				after = append(after, task.ID)
			}
			if strings.Join(original, ",") != strings.Join(after, ",") {
				t.Fatalf("SortTasks mutated its input: %v -> %v", original, after)
			}
		})
	}
}

func TestRenderMarkdownTagMetadata(t *testing.T) {
	md := RenderMarkdown(nil, RenderOptions{
		Filter:      Filter{Tag: " casa ", Status: "all"},
		Counts:      Counts{},
		GeneratedAt: "2026-11-22 15:00",
	})
	if !strings.HasPrefix(md, "# Todos — #casa\n") || !strings.Contains(md, "> tag: #casa\n") {
		t.Fatalf("missing normalized tag metadata: %s", md)
	}
}

func TestRenderFlatEmpty(t *testing.T) {
	var b strings.Builder
	renderFlat(&b, nil, nil, "all", "casa")
	if b.String() != "_No hay tareas._\n" {
		t.Fatalf("renderFlat = %q", b.String())
	}
}

func TestRenderMarkdownDetails(t *testing.T) {
	tasks := []Task{
		{ID: "1", Title: "Comprar leche", Tags: []string{"casa"}, Details: "Llamar a la tienda\nUsar #cupón *hoy*", CreatedAt: "2026-11-22T10:00:00Z", UpdatedAt: "2026-11-22T10:00:00Z", Revision: 1},
		{ID: "2", Title: "Pagar", Details: "Sin etiqueta", CreatedAt: "2026-11-22T11:00:00Z", UpdatedAt: "2026-11-22T11:00:00Z", Revision: 1},
	}
	md := RenderMarkdown(tasks, RenderOptions{
		Filter:      Filter{Status: "all"},
		Counts:      ComputeCounts(tasks),
		GeneratedAt: "2026-11-22 15:00",
	})
	if !strings.Contains(md, "- [ ] Comprar leche\n  Llamar a la tienda\n  Usar \\#cupón \\*hoy\\*\n") {
		t.Fatalf("grouped details missing or wrongly escaped: %s", md)
	}

	flat := RenderMarkdown(tasks, RenderOptions{
		Filter:      Filter{Tag: "casa", Status: "all"},
		Counts:      ComputeCounts(tasks),
		GeneratedAt: "2026-11-22 15:00",
	})
	if !strings.Contains(flat, "- [ ] Comprar leche\n  Llamar a la tienda\n") {
		t.Fatalf("flat details missing: %s", flat)
	}
}
