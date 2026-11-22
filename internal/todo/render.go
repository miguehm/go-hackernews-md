package todo

import (
	"fmt"
	"sort"
	"strings"
)

type RenderOptions struct {
	Filter      Filter
	Counts      Counts
	GeneratedAt string
	Truncated   bool
}

func FilterTasks(tasks []Task, filter Filter) []Task {
	f := filter.Normalized()
	out := make([]Task, 0, len(tasks))
	for _, task := range tasks {
		if f.Tag != "" && !containsTag(task.Tags, f.Tag) {
			continue
		}
		if f.Status == "open" && task.Done {
			continue
		}
		if f.Status == "done" && !task.Done {
			continue
		}
		out = append(out, CloneTask(task))
	}
	return SortTasks(out)
}

func SortTasks(tasks []Task) []Task {
	out := CloneTasks(tasks)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Done != b.Done {
			return !a.Done
		}
		if !a.Done {
			ta, tb := primaryTag(a), primaryTag(b)
			if ta == "" && tb != "" {
				return false
			}
			if ta != "" && tb == "" {
				return true
			}
			if ta != tb {
				return ta < tb
			}
			if a.CreatedAt != b.CreatedAt {
				return a.CreatedAt < b.CreatedAt
			}
			return a.ID < b.ID
		}

		if a.DoneAt == nil && b.DoneAt != nil {
			return false
		}
		if a.DoneAt != nil && b.DoneAt == nil {
			return true
		}
		if a.DoneAt != nil && b.DoneAt != nil && *a.DoneAt != *b.DoneAt {
			return *a.DoneAt > *b.DoneAt
		}
		return a.ID < b.ID
	})
	return out
}

func RenderMarkdown(tasks []Task, options RenderOptions) string {
	f := options.Filter.Normalized()
	ordered := SortTasks(tasks)

	var b strings.Builder
	heading := "# Todos"
	if f.Tag != "" {
		heading += " — #" + f.Tag
	}
	fmt.Fprintf(&b, "%s\n\n", heading)
	fmt.Fprintf(&b, "> %d abiertas · %d hechas · %s\n", options.Counts.Open, options.Counts.Done, options.GeneratedAt)
	if f.Tag != "" {
		fmt.Fprintf(&b, "> tag: #%s\n", f.Tag)
	}
	if options.Truncated {
		b.WriteString("> Lista truncada.\n")
	}
	b.WriteString("\n")

	open, done := splitByStatus(ordered)
	if f.Tag != "" {
		renderFlat(&b, open, done, f.Status, f.Tag)
	} else {
		renderGroups(&b, open, done, f.Status)
	}

	return strings.TrimRight(b.String(), "\n") + "\n"
}

func renderFlat(b *strings.Builder, open, done []Task, status, skipTag string) {
	if len(open) == 0 && (status != "all" || len(done) == 0) {
		b.WriteString("_No hay tareas._\n")
		return
	}
	for _, task := range open {
		writeTask(b, task, skipTag)
	}
	if status != "open" && len(done) > 0 {
		if len(open) > 0 {
			b.WriteString("\n")
		}
		for _, task := range done {
			writeTask(b, task, skipTag)
		}
	}
}

func renderGroups(b *strings.Builder, open, done []Task, status string) {
	wroteSection := false
	current := ""
	for _, task := range open {
		tag := primaryTag(task)
		if !wroteSection || tag != current {
			if wroteSection {
				b.WriteString("\n")
			}
			if tag == "" {
				b.WriteString("## Sin etiqueta\n")
			} else {
				fmt.Fprintf(b, "## #%s\n", tag)
			}
			current = tag
			wroteSection = true
		}
		writeTask(b, task, tag)
	}

	if status != "open" && len(done) > 0 {
		if wroteSection {
			b.WriteString("\n")
		}
		b.WriteString("## Hechas\n")
		for _, task := range done {
			writeTask(b, task, primaryTag(task))
		}
		wroteSection = true
	}
	if !wroteSection {
		b.WriteString("_No hay tareas._\n")
	}
}

func writeTask(b *strings.Builder, task Task, skipTag string) {
	marker := "[ ]"
	if task.Done {
		marker = "[x]"
	}
	fmt.Fprintf(b, "- %s %s", marker, escapeMarkdown(task.Title))
	for _, tag := range task.Tags {
		if skipTag != "" && tag == skipTag {
			continue
		}
		fmt.Fprintf(b, " #%s", tag)
	}
	b.WriteString("\n")
}

func splitByStatus(tasks []Task) ([]Task, []Task) {
	var open, done []Task
	for _, task := range tasks {
		if task.Done {
			done = append(done, task)
		} else {
			open = append(open, task)
		}
	}
	return open, done
}

func primaryTag(task Task) string {
	if len(task.Tags) == 0 {
		return ""
	}
	return task.Tags[0]
}

func containsTag(tags []string, wanted string) bool {
	for _, tag := range tags {
		if tag == wanted {
			return true
		}
	}
	return false
}

func escapeMarkdown(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	var b strings.Builder
	for _, r := range value {
		switch r {
		case '\\', '`', '*', '_', '[', ']', '<', '>', '#':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
