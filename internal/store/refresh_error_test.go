package store

import (
	"testing"
	"time"
)

func TestRefreshErrorLifecycle(t *testing.T) {
	st := New()
	if _, ok := st.LastRefreshError(); ok {
		t.Fatal("new store must have no refresh error")
	}
	at := time.Date(2026, 11, 27, 10, 0, 0, 0, time.FixedZone("CST", -6*3600))
	st.RecordRefreshError("hn_unavailable", at)
	got, ok := st.LastRefreshError()
	if !ok || got.Message != "hn_unavailable" || !got.At.Equal(at) || got.At.Location() != time.UTC {
		t.Fatalf("LastRefreshError = %+v, %v", got, ok)
	}

	st.Set(&Snapshot{Ready: false})
	if _, ok := st.LastRefreshError(); !ok {
		t.Fatal("a non-ready snapshot must not clear the error")
	}
	st.Set(&Snapshot{Ready: true})
	if _, ok := st.LastRefreshError(); ok {
		t.Fatal("a ready snapshot must clear the error")
	}
}
