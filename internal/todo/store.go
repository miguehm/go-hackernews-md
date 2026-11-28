package todo

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type storeFile struct {
	SchemaVersion int    `json:"schema_version"`
	UpdatedAt     string `json:"updated_at"`
	Tasks         []Task `json:"tasks"`
}

type Store struct {
	mu        sync.RWMutex
	file      string
	backup    string
	tasks     []Task
	maxTasks  int
	updatedAt time.Time
}

type RevisionConflictError struct {
	CurrentRevision int
}

func (e *RevisionConflictError) Error() string {
	return "task revision conflict"
}

func NewStore(file string, maxTasks int) (*Store, error) {
	if file == "" {
		return nil, errors.New("todo store file is empty")
	}
	if maxTasks <= 0 {
		maxTasks = 1000
	}
	s := &Store{
		file:     file,
		backup:   file + ".bak",
		tasks:    []Task{},
		maxTasks: maxTasks,
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		return nil, fmt.Errorf("create todo data dir: %w", err)
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.file)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		backup, backupErr := os.ReadFile(s.backup)
		if backupErr != nil {
			return fmt.Errorf("read todo store: %w", err)
		}
		slog.Warn("todo store unreadable, using backup", "file", s.file, "error", err)
		data = backup
	}

	var file storeFile
	if err := json.Unmarshal(data, &file); err != nil {
		backup, backupErr := os.ReadFile(s.backup)
		if backupErr != nil || json.Unmarshal(backup, &file) != nil {
			return fmt.Errorf("decode todo store: %w", err)
		}
		slog.Warn("todo store corrupt, using backup", "file", s.file, "error", err)
	}
	if file.SchemaVersion != 1 {
		return fmt.Errorf("unsupported todo store schema version %d", file.SchemaVersion)
	}

	seen := make(map[string]bool, len(file.Tasks))
	for i := range file.Tasks {
		t := &file.Tasks[i]
		if t.ID == "" || seen[t.ID] {
			return fmt.Errorf("todo store has duplicate or empty task id %q", t.ID)
		}
		seen[t.ID] = true
		tags, err := NormalizeTags(t.Tags)
		if err != nil {
			return fmt.Errorf("task %s has invalid tags: %w", t.ID, err)
		}
		t.Tags = tags
		dueDate, err := NormalizeDueDate(t.DueDate)
		if err != nil {
			return fmt.Errorf("task %s has invalid due_date: %w", t.ID, err)
		}
		t.DueDate = dueDate
		if t.CreatedAt == "" {
			t.CreatedAt = nowString()
		}
		if t.UpdatedAt == "" {
			t.UpdatedAt = t.CreatedAt
		}
		if t.Revision < 1 {
			t.Revision = 1
		}
	}
	s.tasks = append([]Task(nil), file.Tasks...)
	if parsed, err := time.Parse(time.RFC3339, file.UpdatedAt); err == nil {
		s.updatedAt = parsed.UTC()
	}
	if s.updatedAt.IsZero() {
		s.updatedAt = latestTaskUpdate(s.tasks)
	}
	return nil
}

func latestTaskUpdate(tasks []Task) time.Time {
	var latest time.Time
	for _, task := range tasks {
		parsed, err := time.Parse(time.RFC3339, task.UpdatedAt)
		if err != nil {
			continue
		}
		if parsed.After(latest) {
			latest = parsed
		}
	}
	return latest
}

func (s *Store) Snapshot() []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return CloneTasks(s.tasks)
}

func (s *Store) Get(id string) (Task, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	idx := s.indexOf(id)
	if idx < 0 {
		return Task{}, false
	}
	return CloneTask(s.tasks[idx]), true
}

func (s *Store) LastModified() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.updatedAt
}

func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.tasks)
}

func (s *Store) Create(task Task) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.insertLocked(task)
}

// CreateWithID inserts a task unless its ID already exists. The second return
// value reports whether the task was created; false means an existing task was
// returned unchanged.
func (s *Store) CreateWithID(task Task) (Task, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx := s.indexOf(task.ID); idx >= 0 {
		return CloneTask(s.tasks[idx]), false, nil
	}
	created, err := s.insertLocked(task)
	if err != nil {
		return Task{}, false, err
	}
	return created, true, nil
}

func (s *Store) insertLocked(task Task) (Task, error) {
	if len(s.tasks) >= s.maxTasks {
		return Task{}, ErrTaskLimit
	}
	next := CloneTask(task)
	s.tasks = append(s.tasks, next)
	previousUpdatedAt := s.updatedAt
	s.touchLocked()
	if err := s.saveLocked(); err != nil {
		s.tasks = s.tasks[:len(s.tasks)-1]
		s.updatedAt = previousUpdatedAt
		return Task{}, err
	}
	return CloneTask(next), nil
}

func (s *Store) Update(id string, mutate func(*Task) error) (Task, error) {
	return s.UpdateIfRevision(id, nil, true, mutate)
}

func (s *Store) UpdateIfRevision(id string, expected []int, force bool, mutate func(*Task) error) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.indexOf(id)
	if idx < 0 {
		return Task{}, ErrNotFound
	}
	current := s.tasks[idx]
	if !force && !revisionMatches(current.Revision, expected) {
		return Task{}, &RevisionConflictError{CurrentRevision: current.Revision}
	}
	next := CloneTask(current)
	if err := mutate(&next); err != nil {
		return Task{}, err
	}
	next.ID = id
	next.Revision = current.Revision + 1
	previousUpdatedAt := s.updatedAt
	now := s.touchLocked()
	next.UpdatedAt = now.Format(time.RFC3339)

	previous := s.tasks[idx]
	s.tasks[idx] = next
	if err := s.saveLocked(); err != nil {
		s.tasks[idx] = previous
		s.updatedAt = previousUpdatedAt
		return Task{}, err
	}
	return CloneTask(next), nil
}

func (s *Store) Delete(id string) error {
	return s.DeleteIfRevision(id, nil, true)
}

func (s *Store) DeleteIfRevision(id string, expected []int, force bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.indexOf(id)
	if idx < 0 {
		return nil
	}
	if !force && !revisionMatches(s.tasks[idx].Revision, expected) {
		return &RevisionConflictError{CurrentRevision: s.tasks[idx].Revision}
	}
	previous := append([]Task(nil), s.tasks...)
	previousUpdatedAt := s.updatedAt
	s.tasks = append(s.tasks[:idx], s.tasks[idx+1:]...)
	s.touchLocked()
	if err := s.saveLocked(); err != nil {
		s.tasks = previous
		s.updatedAt = previousUpdatedAt
		return err
	}
	return nil
}

func revisionMatches(current int, expected []int) bool {
	for _, revision := range expected {
		if revision == current {
			return true
		}
	}
	return false
}

func (s *Store) touchLocked() time.Time {
	now := time.Now().UTC()
	if !s.updatedAt.IsZero() && !now.After(s.updatedAt) {
		now = s.updatedAt.Add(time.Second)
	}
	s.updatedAt = now
	return now
}

func (s *Store) indexOf(id string) int {
	for i := range s.tasks {
		if s.tasks[i].ID == id {
			return i
		}
	}
	return -1
}

func (s *Store) saveLocked() error {
	dir := filepath.Dir(s.file)
	tmp, err := os.CreateTemp(dir, ".todos-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp todo store: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	updatedAt := s.updatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	file := storeFile{
		SchemaVersion: 1,
		UpdatedAt:     updatedAt.Format(time.RFC3339),
		Tasks:         s.tasks,
	}
	encoder := json.NewEncoder(tmp)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(file); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("encode todo store: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync todo store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close todo store: %w", err)
	}

	if _, err := os.Stat(s.file); err == nil {
		_ = os.Remove(s.backup)
		if err := os.Rename(s.file, s.backup); err != nil {
			return fmt.Errorf("backup todo store: %w", err)
		}
	}
	if err := os.Rename(tmpName, s.file); err != nil {
		return fmt.Errorf("replace todo store: %w", err)
	}
	if d, err := os.Open(dir); err == nil { //nolint:gosec // G304: dir comes from server config, not user input
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339)
}
