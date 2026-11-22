package todo

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeStoreFile(t *testing.T, path string, file storeFile) {
	t.Helper()
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestNewStoreValidation(t *testing.T) {
	if _, err := NewStore("", 10); err == nil || err.Error() != "todo store file is empty" {
		t.Fatalf("empty path error = %v", err)
	}

	file := filepath.Join(t.TempDir(), "todos.json")
	st, err := NewStore(file, 0)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if st.maxTasks != 1000 {
		t.Fatalf("maxTasks = %d, want default 1000", st.maxTasks)
	}

	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("create blocker: %v", err)
	}
	if _, err := NewStore(filepath.Join(blocker, "sub", "todos.json"), 10); err == nil || !strings.Contains(err.Error(), "create todo data dir") {
		t.Fatalf("MkdirAll failure error = %v", err)
	}
}

func TestStoreDetailsBackwardCompatibility(t *testing.T) {
	file := filepath.Join(t.TempDir(), "todos.json")
	raw := `{"schema_version":1,"updated_at":"2026-11-22T10:00:00Z","tasks":[{"id":"old1","title":"Old task","tags":["casa"],"done":false,"created_at":"2026-11-22T10:00:00Z","updated_at":"2026-11-22T10:00:00Z","revision":1}]}`
	if err := os.WriteFile(file, []byte(raw), 0o600); err != nil {
		t.Fatalf("write store: %v", err)
	}
	st, err := NewStore(file, 10)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	old, ok := st.Get("old1")
	if !ok || old.Details != "" {
		t.Fatalf("legacy task details = %q, want empty", old.Details)
	}

	newTask := Task{ID: "new1", Title: "New", Tags: []string{}, Details: "line one\nline two", CreatedAt: "2026-11-22T11:00:00Z", UpdatedAt: "2026-11-22T11:00:00Z", Revision: 1}
	if _, err := st.Create(newTask); err != nil {
		t.Fatalf("Create: %v", err)
	}
	reloaded, err := NewStore(file, 10)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, ok := reloaded.Get("new1")
	if !ok || got.Details != newTask.Details {
		t.Fatalf("reloaded details = %q, want %q", got.Details, newTask.Details)
	}
}

func TestNewStoreInvalidSchema(t *testing.T) {
	file := filepath.Join(t.TempDir(), "todos.json")
	writeStoreFile(t, file, storeFile{SchemaVersion: 2})
	if _, err := NewStore(file, 10); err == nil || !strings.Contains(err.Error(), "unsupported todo store schema version 2") {
		t.Fatalf("schema error = %v", err)
	}
}

func TestStoreLoadBackupFallback(t *testing.T) {
	t.Run("corrupt main file", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "todos.json")
		st, err := NewStore(file, 10)
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		if _, err := st.Create(makeTask("1", "from backup")); err != nil {
			t.Fatalf("Create: %v", err)
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read store: %v", err)
		}
		if err := os.WriteFile(file+".bak", data, 0o600); err != nil {
			t.Fatalf("write backup: %v", err)
		}
		if err := os.WriteFile(file, []byte("{corrupt"), 0o600); err != nil {
			t.Fatalf("corrupt main: %v", err)
		}
		reloaded, err := NewStore(file, 10)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		got, ok := reloaded.Get("1")
		if !ok || got.Title != "from backup" {
			t.Fatalf("task = %+v, %v; want backup content", got, ok)
		}
	})

	t.Run("unreadable main file", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "todos.json")
		writeStoreFile(t, file+".bak", storeFile{
			SchemaVersion: 1,
			Tasks:         []Task{makeTask("7", "recovered")},
		})
		if err := os.Mkdir(file, 0o750); err != nil {
			t.Fatalf("make main a directory: %v", err)
		}
		reloaded, err := NewStore(file, 10)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if got, ok := reloaded.Get("7"); !ok || got.Title != "recovered" {
			t.Fatalf("task = %+v, %v; want backup content", got, ok)
		}
	})

	t.Run("both files corrupt", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "todos.json")
		if err := os.WriteFile(file, []byte("{bad"), 0o600); err != nil {
			t.Fatalf("write main: %v", err)
		}
		if err := os.WriteFile(file+".bak", []byte("{bad"), 0o600); err != nil {
			t.Fatalf("write backup: %v", err)
		}
		if _, err := NewStore(file, 10); err == nil || !strings.Contains(err.Error(), "decode todo store") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestStoreLoadRepairsFields(t *testing.T) {
	file := filepath.Join(t.TempDir(), "todos.json")
	writeStoreFile(t, file, storeFile{
		SchemaVersion: 1,
		Tasks: []Task{
			{ID: "1", Title: "First", Tags: []string{" CASA "}},
		},
	})
	st, err := NewStore(file, 10)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	got, ok := st.Get("1")
	if !ok {
		t.Fatal("task missing")
	}
	if got.CreatedAt == "" || got.UpdatedAt != got.CreatedAt || got.Revision != 1 {
		t.Fatalf("repaired task = %+v", got)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "casa" {
		t.Fatalf("tags = %v", got.Tags)
	}
}

func TestStoreLoadInvalidTasks(t *testing.T) {
	tests := []struct {
		name  string
		tasks []Task
	}{
		{name: "empty id", tasks: []Task{{ID: "", Title: "x"}}},
		{name: "duplicate id", tasks: []Task{{ID: "1", Title: "a"}, {ID: "1", Title: "b"}}},
		{name: "invalid tags", tasks: []Task{{ID: "1", Tags: []string{"bad tag"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "todos.json")
			writeStoreFile(t, file, storeFile{SchemaVersion: 1, Tasks: tt.tasks})
			if _, err := NewStore(file, 10); err == nil {
				t.Fatal("expected load error")
			}
		})
	}
}

func TestStoreLastModifiedSkipsInvalidDate(t *testing.T) {
	file := filepath.Join(t.TempDir(), "todos.json")
	writeStoreFile(t, file, storeFile{
		SchemaVersion: 1,
		Tasks: []Task{
			{ID: "1", Title: "bad", UpdatedAt: "not-a-date"},
			{ID: "2", Title: "good", UpdatedAt: "2026-11-22T10:00:00Z"},
		},
	})
	st, err := NewStore(file, 10)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	want := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	if got := st.LastModified(); !got.Equal(want) {
		t.Fatalf("LastModified = %v, want %v", got, want)
	}
}

func TestStoreUpdateErrors(t *testing.T) {
	st := newTestStore(t, 10)
	if _, err := st.Update("missing", func(task *Task) error { return nil }); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id error = %v, want ErrNotFound", err)
	}
	if _, err := st.Create(makeTask("1", "one")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := st.Update("1", func(task *Task) error { return errors.New("mutate failed") }); err == nil {
		t.Fatal("expected mutate error")
	}
	got, _ := st.Get("1")
	if got.Revision != 1 || got.Title != "one" {
		t.Fatalf("state changed after mutate error: %+v", got)
	}
	updated, err := st.Update("1", func(task *Task) error {
		task.ID = "changed"
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.ID != "1" {
		t.Fatalf("id = %q, want 1", updated.ID)
	}
}

func TestStoreSnapshotIsolation(t *testing.T) {
	st := newTestStore(t, 10)
	if _, err := st.Create(makeTask("1", "one")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	snap := st.Snapshot()
	snap[0].Title = "mutated"
	if got, _ := st.Get("1"); got.Title != "one" {
		t.Fatalf("Snapshot returned aliased tasks: %+v", got)
	}
}

func breakStoreDir(t *testing.T, st *Store) {
	t.Helper()
	dir := filepath.Dir(st.file)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove dir: %v", err)
	}
	if err := os.WriteFile(dir, []byte("blocker"), 0o600); err != nil {
		t.Fatalf("replace dir with file: %v", err)
	}
}

func TestStoreWriteFailuresRollback(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		st := newTestStore(t, 10)
		breakStoreDir(t, st)
		if _, err := st.Create(makeTask("1", "one")); err == nil || !strings.Contains(err.Error(), "create temp todo store") {
			t.Fatalf("Create error = %v", err)
		}
		if len(st.Snapshot()) != 0 {
			t.Fatalf("task was not rolled back: %+v", st.Snapshot())
		}
	})

	t.Run("update", func(t *testing.T) {
		st := newTestStore(t, 10)
		if _, err := st.Create(makeTask("1", "one")); err != nil {
			t.Fatalf("Create: %v", err)
		}
		breakStoreDir(t, st)
		if _, err := st.Update("1", func(task *Task) error { task.Title = "changed"; return nil }); err == nil {
			t.Fatal("expected save error")
		}
		if got, _ := st.Get("1"); got.Title != "one" || got.Revision != 1 {
			t.Fatalf("update was not rolled back: %+v", got)
		}
	})

	t.Run("delete", func(t *testing.T) {
		st := newTestStore(t, 10)
		if _, err := st.Create(makeTask("1", "one")); err != nil {
			t.Fatalf("Create: %v", err)
		}
		breakStoreDir(t, st)
		if err := st.Delete("1"); err == nil {
			t.Fatal("expected save error")
		}
		if got, ok := st.Get("1"); !ok || got.Title != "one" {
			t.Fatalf("delete was not rolled back: %+v, %v", got, ok)
		}
	})
}

func TestStoreBackupRotation(t *testing.T) {
	st := newTestStore(t, 10)
	if _, err := st.Create(makeTask("1", "one")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := st.Create(makeTask("2", "two")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	backup, err := os.ReadFile(st.backup)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if !strings.Contains(string(backup), `"id":"1"`) || strings.Contains(string(backup), `"id":"2"`) {
		t.Fatalf("backup = %s, want previous version only", backup)
	}
}
