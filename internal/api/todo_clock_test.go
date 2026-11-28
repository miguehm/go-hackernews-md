package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Each read must use a single reference time; otherwise a request crossing
// midnight could mix due_soon/overdue counters from two different days.
func TestTodoReadsUseOneClockReadPerRequest(t *testing.T) {
	h := newTodoTestHandler(t, true)
	seedTodoTasks(t, h)
	mux := todoTestMux(h)

	calls := 0
	base := time.Date(2026, 11, 27, 23, 59, 59, 0, time.UTC)
	h.now = func() time.Time {
		calls++
		return base.Add(time.Duration(calls) * time.Second)
	}

	for _, path := range []string{"/v1/todos?status=all", "/v1/todos/tags", "/v1/todos/top?tags=true", "/v1/todos.md", "/v1/todos/tags/trabajo.md"} {
		calls = 0
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, body = %s", path, rr.Code, rr.Body)
		}
		if calls != 1 {
			t.Errorf("%s: clock read %d times, want 1", path, calls)
		}
	}
}
