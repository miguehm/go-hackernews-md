package todo

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	MaxTags         = 10
	MaxTagLength    = 32
	MaxTextBytes    = 300
	MaxTitleSize    = 200
	MaxDetailsBytes = 1000

	// DueDateLayout is the canonical, timezone-free wire format for deadlines.
	DueDateLayout  = "2006-01-02"
	DefaultDueDays = 3
	MaxDueDays     = 30
)

var ErrNotFound = errors.New("task not found")
var ErrTaskLimit = errors.New("task limit reached")

const errTextLength = "text must be between 1 and 300 characters"

var tagPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

const tagDelimiters = ".,;:!?)]}\"'"

func isTagChar(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-'
}

func isTagBoundary(runes []rune, i int) bool {
	if i >= len(runes) {
		return true
	}
	r := runes[i]
	return unicode.IsSpace(r) || strings.ContainsRune(tagDelimiters, r)
}

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

func validationError(field, message string) error {
	return &ValidationError{Field: field, Message: message}
}

type Task struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Tags      []string `json:"tags"`
	Details   string   `json:"details,omitempty"`
	DueDate   *string  `json:"due_date,omitempty"`
	Done      bool     `json:"done"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
	DoneAt    *string  `json:"done_at"`
	Revision  int      `json:"revision"`
}

type Counts struct {
	Open    int `json:"open"`
	Done    int `json:"done"`
	Total   int `json:"total"`
	DueSoon int `json:"due_soon"`
	Overdue int `json:"overdue"`
}

type TagCount struct {
	Tag     string `json:"tag"`
	Open    int    `json:"open"`
	Done    int    `json:"done"`
	Total   int    `json:"total"`
	DueSoon int    `json:"due_soon"`
	Overdue int    `json:"overdue"`
}

type Filter struct {
	Tag      string
	Status   string
	DueDays  int
	Location *time.Location
}

// CountOptions controls the reference date and horizon used for the deadline
// counters. A zero value means "now, with the default due-soon window".
type CountOptions struct {
	Now      time.Time
	DueDays  int
	Location *time.Location
}

func (o CountOptions) normalized() CountOptions {
	if o.Location == nil {
		o.Location = time.UTC
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	o.Now = o.Now.In(o.Location)
	if o.DueDays <= 0 {
		o.DueDays = DefaultDueDays
	}
	return o
}

func (f Filter) Normalized() Filter {
	f.Tag = strings.ToLower(strings.TrimSpace(f.Tag))
	switch f.Status {
	case "done", "all", "due_soon", "overdue":
	default:
		f.Status = "open"
	}
	if f.DueDays <= 0 {
		f.DueDays = DefaultDueDays
	}
	return f
}

// NormalizeDueDate accepts only canonical YYYY-MM-DD dates. Both nil and an
// empty string mean "no deadline"; callers can therefore use the same helper
// for create, patch and legacy store records.
func NormalizeDueDate(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, nil
	}
	parsed, err := time.Parse(DueDateLayout, trimmed)
	if err != nil || parsed.Format(DueDateLayout) != trimmed {
		return nil, validationError("due_date", "due_date must be a valid YYYY-MM-DD date")
	}
	return &trimmed, nil
}

// DueDateOf exposes a stored deadline as a comparable ISO date string. Dates
// are validated on write, so an invalid value is treated as "no deadline"
// rather than crashing the readers.
func DueDateOf(t Task) (string, bool) {
	if t.DueDate == nil {
		return "", false
	}
	value := *t.DueDate
	if _, err := time.Parse(DueDateLayout, value); err != nil {
		return "", false
	}
	return value, true
}

// isOverdue / isDueSoon compare ISO date strings lexicographically, which is
// equivalent to comparing calendar dates and avoids timezone drift inside a
// day. `today` and the horizon are always expressed in UTC.
func isOverdue(due, today string) bool {
	return due < today
}

func isDueSoon(due, today, limit string) bool {
	return due >= today && due <= limit
}

func NormalizeTags(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, raw := range values {
		tag := strings.ToLower(strings.TrimSpace(raw))
		if tag == "" {
			continue
		}
		if len(tag) > MaxTagLength || !tagPattern.MatchString(tag) {
			return nil, validationError("tags", fmt.Sprintf("invalid tag %q", raw))
		}
		if !seen[tag] {
			seen[tag] = true
			out = append(out, tag)
		}
	}
	if len(out) > MaxTags {
		return nil, validationError("tags", fmt.Sprintf("at most %d tags are allowed", MaxTags))
	}
	return out, nil
}

func NormalizeDetails(details string) string {
	details = strings.ReplaceAll(details, "\r\n", "\n")
	details = strings.ReplaceAll(details, "\r", "\n")
	return strings.TrimRight(details, " \t\n")
}

func ValidateDetails(details string) error {
	if len(details) > MaxDetailsBytes {
		return validationError("details", fmt.Sprintf("details must be at most %d bytes", MaxDetailsBytes))
	}
	return nil
}

func extractHashtags(text string) ([]rune, []string) {
	runes := []rune(text)
	out := make([]rune, 0, len(runes))
	var tags []string
	for i := 0; i < len(runes); {
		r := runes[i]
		if r == '#' && (i == 0 || unicode.IsSpace(runes[i-1])) {
			start := i + 1
			end := start
			for end < len(runes) && end-start < MaxTagLength && isTagChar(runes[end]) {
				end++
			}
			if end > start && isTagBoundary(runes, end) {
				tags = append(tags, strings.ToLower(string(runes[start:end])))
				i = end
				continue
			}
		}
		out = append(out, r)
		i++
	}
	return out, tags
}

func ParseText(text string, extra []string) (string, []string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil, validationError("text", "text is required")
	}
	if len(text) > MaxTextBytes {
		return "", nil, validationError("text", errTextLength)
	}

	titleRunes, parsed := extractHashtags(text)
	title := strings.Trim(strings.Join(strings.Fields(string(titleRunes)), " "), " \t\n.,;:")
	if title == "" {
		return "", nil, validationError("text", "title is required")
	}
	if len(title) > MaxTitleSize {
		return "", nil, validationError("text", fmt.Sprintf("title must be at most %d characters", MaxTitleSize))
	}

	tags, err := NormalizeTags(append(parsed, extra...))
	if err != nil {
		return "", nil, err
	}
	return title, tags, nil
}

func NewID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func CloneTask(t Task) Task {
	cloned := t
	if t.Tags != nil {
		cloned.Tags = append([]string(nil), t.Tags...)
	}
	if t.DoneAt != nil {
		doneAt := *t.DoneAt
		cloned.DoneAt = &doneAt
	}
	if t.DueDate != nil {
		dueDate := *t.DueDate
		cloned.DueDate = &dueDate
	}
	return cloned
}

func CloneTasks(tasks []Task) []Task {
	out := make([]Task, len(tasks))
	for i, t := range tasks {
		out[i] = CloneTask(t)
	}
	return out
}

// ComputeCounts keeps its original signature for callers that do not care
// about deadlines (tests, Markdown truncation). Handlers use ComputeCountsWith
// so the due-soon window matches the one requested by the client.
func ComputeCounts(tasks []Task) Counts {
	return ComputeCountsWith(tasks, CountOptions{})
}

func ComputeCountsWith(tasks []Task, options CountOptions) Counts {
	opts := options.normalized()
	today := opts.Now.Format(DueDateLayout)
	limit := opts.Now.AddDate(0, 0, opts.DueDays).Format(DueDateLayout)

	var out Counts
	for _, t := range tasks {
		out.Total++
		if t.Done {
			out.Done++
			continue
		}
		out.Open++
		if due, ok := DueDateOf(t); ok {
			switch {
			case isOverdue(due, today):
				out.Overdue++
			case isDueSoon(due, today, limit):
				out.DueSoon++
			}
		}
	}
	return out
}

func ComputeTagCounts(tasks []Task) ([]TagCount, Counts, Counts) {
	return ComputeTagCountsWith(tasks, CountOptions{})
}

func ComputeTagCountsWith(tasks []Task, options CountOptions) ([]TagCount, Counts, Counts) {
	opts := options.normalized()
	today := opts.Now.Format(DueDateLayout)
	limit := opts.Now.AddDate(0, 0, opts.DueDays).Format(DueDateLayout)

	byTag := make(map[string]*TagCount)
	var untagged Counts

	for _, t := range tasks {
		due, hasDue := DueDateOf(t)
		overdue := hasDue && isOverdue(due, today)
		dueSoon := hasDue && isDueSoon(due, today, limit)

		addDue := func(overdueField, dueSoonField *int) {
			if overdue {
				*overdueField++
			} else if dueSoon {
				*dueSoonField++
			}
		}
		if len(t.Tags) == 0 {
			if t.Done {
				untagged.Done++
			} else {
				untagged.Open++
				addDue(&untagged.Overdue, &untagged.DueSoon)
			}
			untagged.Total++
			continue
		}
		for _, tag := range t.Tags {
			entry, ok := byTag[tag]
			if !ok {
				entry = &TagCount{Tag: tag}
				byTag[tag] = entry
			}
			if t.Done {
				entry.Done++
			} else {
				entry.Open++
				addDue(&entry.Overdue, &entry.DueSoon)
			}
			entry.Total++
		}
	}

	names := make([]string, 0, len(byTag))
	for name := range byTag {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]TagCount, 0, len(names))
	for _, name := range names {
		out = append(out, *byTag[name])
	}
	return out, ComputeCountsWith(tasks, options), untagged
}
