package service

import (
	"context"
	"testing"
	"time"

	"hnmd/internal/hn"
	"hnmd/internal/store"
)

// blockingStoryClient blocks TopStoryIDs until the request context ends.
type blockingStoryClient struct {
	started chan struct{}
}

func (b *blockingStoryClient) TopStoryIDs(ctx context.Context) ([]int, error) {
	close(b.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (b *blockingStoryClient) Item(ctx context.Context, id int) (*hn.Item, error) {
	return nil, ctx.Err()
}

func TestStartRefresherShutdownCancelsInFlightRefresh(t *testing.T) {
	cfg := testConfig()
	cfg.RefreshInterval = time.Hour // a Background-derived ctx would block for an hour
	sc := &blockingStoryClient{started: make(chan struct{})}
	svc := &Service{cfg: cfg, hn: sc, fetcher: &fakeFetcher{}, store: store.New()}

	ctx, cancel := context.WithCancel(context.Background())
	done := svc.StartRefresher(ctx)
	<-sc.started
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("refresher did not stop after context cancellation")
	}
}
