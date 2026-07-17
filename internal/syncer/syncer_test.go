package syncer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/example/memoq/internal/memos"
	"github.com/example/memoq/internal/store"
)

// memoServer is a minimal in-memory Memos server serving GET /api/v1/memos with
// a settable memo list. It is safe for the single-shot fetches the syncer does.
type memoServer struct {
	mu   sync.Mutex
	body string
}

func (m *memoServer) set(body string) {
	m.mu.Lock()
	m.body = body
	m.mu.Unlock()
}

func newSyncEnv(t *testing.T) (*Syncer, *store.Store, *memoServer) {
	t.Helper()
	ms := &memoServer{body: `{"memos":[],"nextPageToken":""}`}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ms.mu.Lock()
		defer ms.mu.Unlock()
		_, _ = w.Write([]byte(ms.body))
	}))
	t.Cleanup(srv.Close)

	st, err := store.Open(filepath.Join(t.TempDir(), "sync.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cl := memos.New(srv.URL, "t")
	return New(cl, st), st, ms
}

func TestSync_AddsNewMemos(t *testing.T) {
	sy, st, ms := newSyncEnv(t)
	ms.set(`{"memos":[
		{"uid":"a","content":"first","visibility":"PRIVATE"},
		{"uid":"b","content":"second","visibility":"PUBLIC"}
	],"nextPageToken":""}`)

	res, err := sy.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.Added != 2 || res.Updated != 0 || res.Deleted != 0 || res.Total != 2 {
		t.Errorf("first sync result = %+v, want Added=2 Total=2", res)
	}
	if m, _ := st.Get("a"); m == nil || m.Content != "first" {
		t.Errorf("memo a not stored correctly: %+v", m)
	}
}

func TestSync_SkipsUnchanged(t *testing.T) {
	sy, _, ms := newSyncEnv(t)
	ms.set(`{"memos":[{"uid":"a","content":"stable","visibility":"PRIVATE"}],"nextPageToken":""}`)

	if _, err := sy.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	// second sync, identical content -> skipped
	res, err := sy.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != 1 || res.Added != 0 || res.Updated != 0 {
		t.Errorf("second sync = %+v, want Skipped=1", res)
	}
}

func TestSync_UpdatesChanged(t *testing.T) {
	sy, st, ms := newSyncEnv(t)
	ms.set(`{"memos":[{"uid":"a","content":"v1","visibility":"PRIVATE"}],"nextPageToken":""}`)
	if _, err := sy.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	ms.set(`{"memos":[{"uid":"a","content":"v2","visibility":"PRIVATE"}],"nextPageToken":""}`)
	res, err := sy.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 1 || res.Added != 0 || res.Skipped != 0 {
		t.Errorf("update sync = %+v, want Updated=1", res)
	}
	if m, _ := st.Get("a"); m.Content != "v2" {
		t.Errorf("content not updated: %q", m.Content)
	}
}

func TestSync_ReconcilesDeletions(t *testing.T) {
	sy, st, ms := newSyncEnv(t)
	ms.set(`{"memos":[
		{"uid":"a","content":"keep","visibility":"PRIVATE"},
		{"uid":"b","content":"remove","visibility":"PRIVATE"}
	],"nextPageToken":""}`)
	if _, err := sy.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	// b disappears from the server
	ms.set(`{"memos":[{"uid":"a","content":"keep","visibility":"PRIVATE"}],"nextPageToken":""}`)
	res, err := sy.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 1 {
		t.Errorf("Deleted = %d, want 1", res.Deleted)
	}
	if m, _ := st.Get("b"); m != nil {
		t.Error("locally deleted memo b still present")
	}
	if m, _ := st.Get("a"); m == nil {
		t.Error("memo a should be kept")
	}
}

func TestSync_MixedAddUpdateSkipDelete(t *testing.T) {
	sy, _, ms := newSyncEnv(t)
	ms.set(`{"memos":[
		{"uid":"keep","content":"same","visibility":"PRIVATE"},
		{"uid":"change","content":"old","visibility":"PRIVATE"},
		{"uid":"gone","content":"bye","visibility":"PRIVATE"}
	],"nextPageToken":""}`)
	if _, err := sy.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	ms.set(`{"memos":[
		{"uid":"keep","content":"same","visibility":"PRIVATE"},
		{"uid":"change","content":"new","visibility":"PRIVATE"},
		{"uid":"fresh","content":"hi","visibility":"PRIVATE"}
	],"nextPageToken":""}`)
	res, err := sy.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 1 || res.Updated != 1 || res.Skipped != 1 || res.Deleted != 1 {
		t.Errorf("mixed sync = %+v, want Added=1 Updated=1 Skipped=1 Deleted=1", res)
	}
}

func TestSync_SetsLastSyncTime(t *testing.T) {
	sy, st, _ := newSyncEnv(t)
	if st.LastSyncUnix() != 0 {
		t.Fatal("precondition: LastSyncUnix should start at 0")
	}
	if _, err := sy.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st.LastSyncUnix() == 0 {
		t.Error("Sync did not record last-sync time")
	}
}

func TestMaybeAutoSync_TTLDisabled(t *testing.T) {
	sy, st, ms := newSyncEnv(t)
	ms.set(`{"memos":[{"uid":"a","content":"x","visibility":"PRIVATE"}],"nextPageToken":""}`)
	res, err := sy.MaybeAutoSync(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if res != nil {
		t.Error("ttl<=0 must disable auto-sync (nil result)")
	}
	if m, _ := st.Get("a"); m != nil {
		t.Error("no sync should have happened with ttl=0")
	}
}

func TestMaybeAutoSync_FreshCacheSkips(t *testing.T) {
	sy, st, _ := newSyncEnv(t)
	if err := st.SetLastSyncNow(); err != nil {
		t.Fatal(err)
	}
	// Large TTL -> cache considered fresh -> skip.
	res, err := sy.MaybeAutoSync(context.Background(), 3600)
	if err != nil {
		t.Fatal(err)
	}
	if res != nil {
		t.Error("fresh cache within TTL should skip sync")
	}
}

func TestMaybeAutoSync_StaleCacheRuns(t *testing.T) {
	sy, _, ms := newSyncEnv(t)
	ms.set(`{"memos":[{"uid":"a","content":"x","visibility":"PRIVATE"}],"nextPageToken":""}`)
	// Never synced -> last=0 -> should run regardless of TTL.
	res, err := sy.MaybeAutoSync(context.Background(), 3600)
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatal("never-synced cache should run a sync")
	}
	if res.Added != 1 {
		t.Errorf("auto-sync Added = %d, want 1", res.Added)
	}
}

func TestSync_ServerErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()
	st, _ := store.Open(filepath.Join(t.TempDir(), "e.db"))
	defer st.Close()
	sy := New(memos.New(srv.URL, "t"), st)
	if _, err := sy.Sync(context.Background()); err == nil {
		t.Error("expected error to propagate from server 500")
	}
}

func TestToStoreMemo_DefaultsVisibilityAndTags(t *testing.T) {
	rm := &memos.Memo{UID: "a", Content: "c"} // no visibility
	m := toStoreMemo(rm, "hash")
	if m.Visibility != "PRIVATE" {
		t.Errorf("visibility default = %q, want PRIVATE", m.Visibility)
	}
	if m.ContentHash != "hash" {
		t.Errorf("hash = %q", m.ContentHash)
	}
}
