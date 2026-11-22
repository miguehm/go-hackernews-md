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
