package api

import (
	"log/slog"
	"net/http"
	"time"

	"hnmd/internal/store"
	"hnmd/internal/todo"
)

// RouterDeps holds everything NewRouter needs to build the HTTP handler.
type RouterDeps struct {
	HN    *store.Store
	Todos *todo.Store

	APIKeys       []string
	ProtectHealth bool
	CORSOrigins   []string

	PublicBaseURL           string
	TodoAllowInsecureWrites bool
	TodoLocation            *time.Location

	Version string
	Logger  *slog.Logger
}

// NewRouter registers every route and wraps the mux in the middleware chain.
// When APIKeys is non-empty, every /v1 route (and /health with ProtectHealth)
// requires a bearer key; CORS preflights are answered before auth.
func NewRouter(d RouterDeps) http.Handler {
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	authEnabled := len(d.APIKeys) > 0

	hn := New(d.HN, DefaultWarmupRetryAfter)
	todos := NewTodoHandlerWithLocation(d.Todos, d.PublicBaseURL, authEnabled || d.TodoAllowInsecureWrites, d.TodoLocation)
	status := NewStatusHandler(d.HN, d.Todos, authEnabled, d.Version)

	protect := func(h http.Handler) http.Handler { return h }
	if authEnabled {
		protect = BearerAPIKeyAuth(d.APIKeys)
	}

	mux := http.NewServeMux()
	routes := []struct {
		pattern string
		handler http.HandlerFunc
	}{
		{"/v1/status", status.ServeHTTP},
		{"/v1/top", hn.Index},
		{"/v1/articles/{itemId}", hn.Article},
		{"/top", hn.Top},
		{"/v1/todos", todos.Index},
		{"/v1/todos.md", todos.Markdown},
		{"/v1/todos/tags", todos.Tags},
		{"/v1/todos/tags/{tag}", todos.TagsMarkdown},
		{"/v1/todos/top", todos.Top},
		{"/v1/todos/tasks", todos.Tasks},
		{"/v1/todos/tasks/{id}", todos.Task},
	}
	for _, route := range routes {
		mux.Handle(route.pattern, protect(route.handler))
	}

	var health http.Handler = http.HandlerFunc(healthHandler)
	if d.ProtectHealth {
		health = protect(health)
	}
	mux.Handle("/health", health)

	// Order matters: Recover wraps Gzip so a panic never leaves a partial
	// response; AccessLog sits outside Recover to log the resulting 500.
	return RequestID(
		AccessLog(logger)(
			Recover(logger)(
				GzipMiddleware(CORS(d.CORSOrigins)(mux)))))
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
