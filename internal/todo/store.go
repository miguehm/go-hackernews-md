package todo

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
	mu       sync.RWMutex
	file     string
	backup   string
	tasks    []Task
	maxTasks int
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
		log.Printf("warning: todo store unreadable, using backup: %v", err)
		data = backup
	}

	var file storeFile
	if err := json.Unmarshal(data, &file); err != nil {
		backup, backupErr := os.ReadFile(s.backup)
		if backupErr != nil || json.Unmarshal(backup, &file) != nil {
			return fmt.Errorf("decode todo store: %w", err)
		}
		log.Printf("warning: todo store corrupt, using backup")
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
	return nil
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
	var latest time.Time
	for _, t := range s.tasks {
		parsed, err := time.Parse(time.RFC3339, t.UpdatedAt)
		if err != nil {
			continue
		}
		if parsed.After(latest) {
			latest = parsed
		}
	}
	return latest
}

func (s *Store) Create(task Task) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.tasks) >= s.maxTasks {
		return Task{}, ErrTaskLimit
	}
	next := CloneTask(task)
	s.tasks = append(s.tasks, next)
	if err := s.saveLocked(); err != nil {
		s.tasks = s.tasks[:len(s.tasks)-1]
		return Task{}, err
	}
	return CloneTask(next), nil
}

func (s *Store) Update(id string, mutate func(*Task) error) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.indexOf(id)
	if idx < 0 {
		return Task{}, ErrNotFound
	}
	next := CloneTask(s.tasks[idx])
	if err := mutate(&next); err != nil {
		return Task{}, err
	}
	next.ID = id
	next.Revision = s.tasks[idx].Revision + 1
	next.UpdatedAt = nowString()

	previous := s.tasks[idx]
	s.tasks[idx] = next
	if err := s.saveLocked(); err != nil {
		s.tasks[idx] = previous
		return Task{}, err
	}
	return CloneTask(next), nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.indexOf(id)
	if idx < 0 {
		return nil
	}
	previous := append([]Task(nil), s.tasks...)
	s.tasks = append(s.tasks[:idx], s.tasks[idx+1:]...)
	if err := s.saveLocked(); err != nil {
		s.tasks = previous
		return err
	}
	return nil
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
	defer os.Remove(tmpName)

	file := storeFile{
		SchemaVersion: 1,
		UpdatedAt:     nowString(),
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
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339)
}
