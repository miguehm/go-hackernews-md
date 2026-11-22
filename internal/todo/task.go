package todo

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const (
	MaxTags         = 10
	MaxTagLength    = 32
	MaxTextBytes    = 300
	MaxTitleSize    = 200
	MaxDetailsBytes = 1000
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
	Done      bool     `json:"done"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
	DoneAt    *string  `json:"done_at"`
	Revision  int      `json:"revision"`
}

type Counts struct {
	Open  int `json:"open"`
	Done  int `json:"done"`
	Total int `json:"total"`
}

type TagCount struct {
	Tag   string `json:"tag"`
	Open  int    `json:"open"`
	Done  int    `json:"done"`
	Total int    `json:"total"`
}

type Filter struct {
	Tag    string
	Status string
}

func (f Filter) Normalized() Filter {
	f.Tag = strings.ToLower(strings.TrimSpace(f.Tag))
	switch f.Status {
	case "done", "all":
	default:
		f.Status = "open"
	}
	return f
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
	return cloned
}

func CloneTasks(tasks []Task) []Task {
	out := make([]Task, len(tasks))
	for i, t := range tasks {
		out[i] = CloneTask(t)
	}
	return out
}

func ComputeCounts(tasks []Task) Counts {
	var out Counts
	for _, t := range tasks {
		out.Total++
		if t.Done {
			out.Done++
		} else {
			out.Open++
		}
	}
	return out
}

func ComputeTagCounts(tasks []Task) ([]TagCount, Counts, Counts) {
	byTag := make(map[string]*TagCount)
	var untagged Counts

	for _, t := range tasks {
		if len(t.Tags) == 0 {
			if t.Done {
				untagged.Done++
			} else {
				untagged.Open++
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
	return out, ComputeCounts(tasks), untagged
}
