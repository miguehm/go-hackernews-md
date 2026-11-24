package todo

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func newTestStore(t *testing.T, maxTasks int) *Store {
	t.Helper()
	file := filepath.Join(t.TempDir(), "todos.json")
	st, err := NewStore(file, maxTasks)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return st
}

func makeTask(id, title string) Task {
	return Task{
		ID:        id,
		Title:     title,
		Tags:      []string{"casa"},
		CreatedAt: "2026-11-22T10:00:00Z",
		UpdatedAt: "2026-11-22T10:00:00Z",
		Revision:  1,
	}
}

func TestStoreCRUD(t *testing.T) {
	st := newTestStore(t, 10)

	created, err := st.Create(makeTask("1", "Comprar leche"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Revision != 1 {
		t.Fatalf("revision = %d, want 1", created.Revision)
	}

	got, ok := st.Get("1")
	if !ok || got.Title != "Comprar leche" {
		t.Fatalf("Get = %+v, %v", got, ok)
	}

	updated, err := st.Update("1", func(task *Task) error {
		task.Done = true
		now := "2026-11-22T11:00:00Z"
		task.DoneAt = &now
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Revision != 2 || !updated.Done || updated.DoneAt == nil || *updated.DoneAt != "2026-11-22T11:00:00Z" {
		t.Fatalf("updated = %+v", updated)
	}

	got.Tags[0] = "mutated"
	got, _ = st.Get("1")
	if got.Tags[0] != "casa" {
		t.Fatalf("Get returned aliased tags: %v", got.Tags)
	}

	if err := st.Delete("1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := st.Get("1"); ok {
		t.Fatal("task still present after delete")
	}
	if err := st.Delete("1"); err != nil {
		t.Fatalf("idempotent Delete: %v", err)
	}
}

func TestStorePersistence(t *testing.T) {
	file := filepath.Join(t.TempDir(), "todos.json")
	st, err := NewStore(file, 10)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := st.Create(makeTask("1", "Comprar leche")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := st.Create(makeTask("2", "Pagar alquiler")); err != nil {
		t.Fatalf("Create: %v", err)
	}

	reloaded, err := NewStore(file, 10)
	if err != nil {
		t.Fatalf("reload NewStore: %v", err)
	}
	snapshot := reloaded.Snapshot()
	if len(snapshot) != 2 || snapshot[0].ID != "1" || snapshot[1].ID != "2" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if reloaded.LastModified().IsZero() {
		t.Fatal("LastModified is zero")
	}
}

func TestStoreLastModifiedDoesNotGoBackwards(t *testing.T) {
	st := newTestStore(t, 10)
	if _, err := st.Create(makeTask("1", "one")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	before := st.LastModified()
	if before.IsZero() {
		t.Fatal("LastModified is zero after create")
	}
	if err := st.Delete("1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	after := st.LastModified()
	if after.Before(before) {
		t.Fatalf("LastModified regressed from %v to %v", before, after)
	}
}

func TestStoreCreateWithIDIsIdempotent(t *testing.T) {
	st := newTestStore(t, 10)
	task := makeTask("client1", "one")
	created, wasCreated, err := st.CreateWithID(task)
	if err != nil || !wasCreated {
		t.Fatalf("first CreateWithID = %+v, %v, %v", created, wasCreated, err)
	}
	existing, wasCreated, err := st.CreateWithID(makeTask("client1", "changed"))
	if err != nil || wasCreated {
		t.Fatalf("second CreateWithID = %+v, %v, %v", existing, wasCreated, err)
	}
	if existing.Title != "one" {
		t.Fatalf("existing title = %q", existing.Title)
	}
	if got := st.Count(); got != 1 {
		t.Fatalf("count = %d", got)
	}
}

func TestStoreConditionalUpdateAndDelete(t *testing.T) {
	st := newTestStore(t, 10)
	if _, err := st.Create(makeTask("1", "one")); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := st.UpdateIfRevision("1", []int{1}, false, func(task *Task) error {
		task.Done = true
		return nil
	}); err != nil {
		t.Fatalf("UpdateIfRevision: %v", err)
	}
	if _, err := st.UpdateIfRevision("1", []int{1}, false, func(task *Task) error {
		task.Title = "stale"
		return nil
	}); err == nil {
		t.Fatal("stale UpdateIfRevision succeeded")
	} else if conflict, ok := err.(*RevisionConflictError); !ok || conflict.CurrentRevision != 2 {
		t.Fatalf("conflict = %#v", err)
	}

	if err := st.DeleteIfRevision("1", []int{1}, false); err == nil {
		t.Fatal("stale DeleteIfRevision succeeded")
	} else if conflict, ok := err.(*RevisionConflictError); !ok || conflict.CurrentRevision != 2 {
		t.Fatalf("delete conflict = %#v", err)
	}
	if err := st.DeleteIfRevision("1", nil, true); err != nil {
		t.Fatalf("forced delete: %v", err)
	}
}

func TestStoreTaskLimit(t *testing.T) {
	st := newTestStore(t, 1)
	if _, err := st.Create(makeTask("1", "one")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := st.Create(makeTask("2", "two")); !errors.Is(err, ErrTaskLimit) {
		t.Fatalf("second Create error = %v, want ErrTaskLimit", err)
	}
}

func TestStoreConcurrentUpdates(t *testing.T) {
	st := newTestStore(t, 100)
	if _, err := st.Create(makeTask("1", "one")); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := st.Update("1", func(task *Task) error {
				task.Title = "updated"
				return nil
			}); err != nil {
				t.Errorf("Update: %v", err)
			}
		}()
	}
	wg.Wait()

	got, ok := st.Get("1")
	if !ok || got.Revision != 21 || got.Title != "updated" {
		t.Fatalf("final task = %+v, %v", got, ok)
	}
}
