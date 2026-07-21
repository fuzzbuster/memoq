package store

import (
	"path/filepath"
	"testing"
)

// newTestStore opens a fresh on-disk SQLite store in a temp dir. A real file
// (not :memory:) is used so WAL and the single-connection settings behave the
// same as in production.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustUpsert(t *testing.T, s *Store, m *Memo) {
	t.Helper()
	if err := s.Upsert(m); err != nil {
		t.Fatalf("Upsert(%s): %v", m.UID, err)
	}
}

func TestUpsertAndGet(t *testing.T) {
	s := newTestStore(t)
	m := &Memo{
		UID: "a1", Content: "hello world", Tags: []string{"work", "todo"},
		Visibility: "PRIVATE", Pinned: true, CreatedTime: 1000, UpdatedTime: 1001,
		ContentHash: "h1",
	}
	mustUpsert(t, s, m)

	got, err := s.Get("a1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil for existing memo")
	}
	if got.Content != "hello world" || got.Visibility != "PRIVATE" || !got.Pinned {
		t.Errorf("unexpected memo: %+v", got)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "work" || got.Tags[1] != "todo" {
		t.Errorf("tags = %v, want [work todo]", got.Tags)
	}
}

func TestGet_NotFoundReturnsNil(t *testing.T) {
	s := newTestStore(t)
	got, err := s.Get("nope")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for missing uid, got %+v", got)
	}
}

func TestUpsert_UpdatesExisting(t *testing.T) {
	s := newTestStore(t)
	mustUpsert(t, s, &Memo{UID: "x", Content: "v1", ContentHash: "h1", Visibility: "PRIVATE"})
	mustUpsert(t, s, &Memo{UID: "x", Content: "v2", ContentHash: "h2", Visibility: "PUBLIC", Tags: []string{"t"}})

	got, _ := s.Get("x")
	if got.Content != "v2" || got.Visibility != "PUBLIC" || got.ContentHash != "h2" {
		t.Errorf("update did not overwrite: %+v", got)
	}
	// still exactly one row
	snapshot, _ := s.MemoHashSnapshot()
	if len(snapshot) != 1 {
		t.Errorf("expected 1 row after upsert-update, got %d", len(snapshot))
	}
}

func TestDelete(t *testing.T) {
	s := newTestStore(t)
	mustUpsert(t, s, &Memo{UID: "d", Content: "bye", Visibility: "PRIVATE"})
	if err := s.Delete("d"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, _ := s.Get("d")
	if got != nil {
		t.Error("memo still present after delete")
	}
	// deleting a nonexistent uid is a no-op, not an error
	if err := s.Delete("ghost"); err != nil {
		t.Errorf("Delete(missing) = %v, want nil", err)
	}
}

func TestDelete_RemovesAttachmentMetadata(t *testing.T) {
	s := newTestStore(t)
	mustUpsert(t, s, &Memo{UID: "d", Content: "bye", Visibility: "PRIVATE"})
	if err := s.UpsertAttachment(&Attachment{UID: "linked", MemoUID: "d"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAttachment(&Attachment{UID: "other", MemoUID: "keep"}); err != nil {
		t.Fatal(err)
	}

	if err := s.Delete("d"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetAttachment("linked"); err != nil || got != nil {
		t.Fatalf("linked attachment survived memo delete: attachment=%+v err=%v", got, err)
	}
	if got, err := s.GetAttachment("other"); err != nil || got == nil {
		t.Fatalf("unrelated attachment was deleted: attachment=%+v err=%v", got, err)
	}
}

func TestMemoHashSnapshot(t *testing.T) {
	s := newTestStore(t)
	mustUpsert(t, s, &Memo{UID: "h", Content: "c", ContentHash: "abc", Visibility: "PRIVATE"})

	snapshot, err := s.MemoHashSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot) != 1 || snapshot["h"] != "abc" {
		t.Errorf("MemoHashSnapshot = %v", snapshot)
	}
}

func TestReconcileSnapshot_PreservesConcurrentChanges(t *testing.T) {
	s := newTestStore(t)
	mustUpsert(t, s, &Memo{UID: "updated", Content: "old", ContentHash: "old", Visibility: "PRIVATE"})
	mustUpsert(t, s, &Memo{UID: "deleted", Content: "old", ContentHash: "old", Visibility: "PRIVATE"})
	baseline, err := s.MemoHashSnapshot()
	if err != nil {
		t.Fatal(err)
	}

	mustUpsert(t, s, &Memo{UID: "updated", Content: "local", ContentHash: "local", Visibility: "PRIVATE"})
	if err := s.Delete("deleted"); err != nil {
		t.Fatal(err)
	}
	mustUpsert(t, s, &Memo{UID: "new", Content: "local", ContentHash: "local", Visibility: "PRIVATE"})

	result, err := s.ReconcileSnapshot(baseline, []*Memo{
		{UID: "updated", Content: "remote", ContentHash: "remote", Visibility: "PRIVATE"},
		{UID: "deleted", Content: "remote", ContentHash: "remote", Visibility: "PRIVATE"},
		{UID: "new", Content: "remote", ContentHash: "remote", Visibility: "PRIVATE"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Preserved != 3 || result.Added != 0 || result.Updated != 0 {
		t.Fatalf("ReconcileSnapshot result = %+v", result)
	}
	if got, _ := s.Get("updated"); got == nil || got.Content != "local" {
		t.Errorf("concurrent update was overwritten: %+v", got)
	}
	if got, _ := s.Get("deleted"); got != nil {
		t.Errorf("concurrent delete was resurrected: %+v", got)
	}
	if got, _ := s.Get("new"); got == nil || got.Content != "local" {
		t.Errorf("concurrent insert was overwritten: %+v", got)
	}
}

func TestReconcileSnapshot_DeletesAttachmentsWithMemo(t *testing.T) {
	s := newTestStore(t)
	mustUpsert(t, s, &Memo{UID: "gone", ContentHash: "old", Visibility: "PRIVATE"})
	if err := s.UpsertAttachment(&Attachment{UID: "linked", MemoUID: "gone"}); err != nil {
		t.Fatal(err)
	}
	baseline, err := s.MemoHashSnapshot()
	if err != nil {
		t.Fatal(err)
	}

	result, err := s.ReconcileSnapshot(baseline, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Deleted != 1 {
		t.Fatalf("ReconcileSnapshot result = %+v", result)
	}
	if memo, _ := s.Get("gone"); memo != nil {
		t.Errorf("memo survived reconciliation: %+v", memo)
	}
	if attachment, _ := s.GetAttachment("linked"); attachment != nil {
		t.Errorf("attachment survived reconciliation: %+v", attachment)
	}
}

func TestList_FiltersAndOrdering(t *testing.T) {
	s := newTestStore(t)
	// created times ascending: old .. new
	mustUpsert(t, s, &Memo{UID: "old", Content: "a", Visibility: "PRIVATE", CreatedTime: 100, Tags: []string{"work"}})
	mustUpsert(t, s, &Memo{UID: "mid", Content: "b", Visibility: "PUBLIC", CreatedTime: 200, Tags: []string{"home"}})
	mustUpsert(t, s, &Memo{UID: "new", Content: "c", Visibility: "PRIVATE", CreatedTime: 300, Tags: []string{"work"}})
	mustUpsert(t, s, &Memo{UID: "pin", Content: "d", Visibility: "PRIVATE", CreatedTime: 50, Pinned: true, Tags: []string{"work"}})

	t.Run("pinned first then newest", func(t *testing.T) {
		got, err := s.List(ListFilter{})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 4 {
			t.Fatalf("got %d memos", len(got))
		}
		if got[0].UID != "pin" {
			t.Errorf("first = %s, want pin (pinned-first)", got[0].UID)
		}
		if got[1].UID != "new" || got[2].UID != "mid" || got[3].UID != "old" {
			t.Errorf("order after pin = %s,%s,%s want new,mid,old", got[1].UID, got[2].UID, got[3].UID)
		}
	})

	t.Run("tag filter", func(t *testing.T) {
		got, _ := s.List(ListFilter{Tag: "work"})
		if len(got) != 3 {
			t.Errorf("tag=work returned %d, want 3", len(got))
		}
		got, _ = s.List(ListFilter{Tag: "home"})
		if len(got) != 1 || got[0].UID != "mid" {
			t.Errorf("tag=home returned %v", got)
		}
	})

	t.Run("visibility filter", func(t *testing.T) {
		got, _ := s.List(ListFilter{Visibility: "PUBLIC"})
		if len(got) != 1 || got[0].UID != "mid" {
			t.Errorf("visibility=PUBLIC returned %v", got)
		}
	})

	t.Run("date range", func(t *testing.T) {
		got, _ := s.List(ListFilter{FromUnix: 150, ToUnix: 250})
		if len(got) != 1 || got[0].UID != "mid" {
			t.Errorf("range [150,250] returned %v", got)
		}
	})

	t.Run("limit and offset", func(t *testing.T) {
		got, _ := s.List(ListFilter{Limit: 2})
		if len(got) != 2 {
			t.Errorf("limit=2 returned %d", len(got))
		}
		page2, _ := s.List(ListFilter{Limit: 2, Offset: 2})
		if len(page2) != 2 {
			t.Errorf("limit=2 offset=2 returned %d", len(page2))
		}
		if got[0].UID == page2[0].UID {
			t.Error("offset did not advance the page")
		}
	})
}

func TestList_TagFilterNoSubstringFalsePositive(t *testing.T) {
	s := newTestStore(t)
	// "work" must not match "workflow": exact whitespace-bounded token match.
	mustUpsert(t, s, &Memo{UID: "a", Content: "x", Visibility: "PRIVATE", Tags: []string{"workflow"}})
	got, _ := s.List(ListFilter{Tag: "work"})
	if len(got) != 0 {
		t.Errorf("tag=work should not match 'workflow', got %v", got)
	}
}

func TestSearch_FTSAndLike(t *testing.T) {
	s := newTestStore(t)
	mustUpsert(t, s, &Memo{UID: "1", Content: "deployment checklist for release", Visibility: "PRIVATE", CreatedTime: 10})
	mustUpsert(t, s, &Memo{UID: "2", Content: "grocery shopping list", Visibility: "PRIVATE", CreatedTime: 20})
	mustUpsert(t, s, &Memo{UID: "3", Content: "同步笔记内容测试", Visibility: "PRIVATE", CreatedTime: 30})

	t.Run("ascii word via FTS", func(t *testing.T) {
		hits, err := s.Search("deployment", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 1 || hits[0].UID != "1" {
			t.Errorf("search 'deployment' = %v, want [1]", hits)
		}
	})

	t.Run("cjk substring via LIKE fallback", func(t *testing.T) {
		hits, err := s.Search("同步", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 1 || hits[0].UID != "3" {
			t.Errorf("search '同步' = %v, want [3]", hits)
		}
	})

	t.Run("no match", func(t *testing.T) {
		hits, err := s.Search("nonexistentxyz", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 0 {
			t.Errorf("expected no hits, got %v", hits)
		}
	})

	t.Run("special chars do not break FTS", func(t *testing.T) {
		// ftsEscape must prevent a syntax error on ':' / '-'.
		if _, err := s.Search("release: v1-2", 10); err != nil {
			t.Errorf("search with special chars errored: %v", err)
		}
	})
}

func TestSearch_LimitDefaultsToTen(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 15; i++ {
		mustUpsert(t, s, &Memo{UID: string(rune('a' + i)), Content: "common term here", Visibility: "PRIVATE", CreatedTime: int64(i)})
	}
	hits, err := s.Search("common", 0) // 0 -> default 10
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 10 {
		t.Errorf("limit=0 returned %d hits, want default 10", len(hits))
	}
}

func TestStats(t *testing.T) {
	s := newTestStore(t)
	mustUpsert(t, s, &Memo{UID: "1", Content: "a", Visibility: "PRIVATE", CreatedTime: 100, Pinned: true, Tags: []string{"work", "urgent"}})
	mustUpsert(t, s, &Memo{UID: "2", Content: "b", Visibility: "PRIVATE", CreatedTime: 300, Tags: []string{"work"}})
	mustUpsert(t, s, &Memo{UID: "3", Content: "c", Visibility: "PRIVATE", CreatedTime: 200})

	st, err := s.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.Total != 3 {
		t.Errorf("Total = %d, want 3", st.Total)
	}
	if st.Pinned != 1 {
		t.Errorf("Pinned = %d, want 1", st.Pinned)
	}
	if st.NewestTS != 300 || st.OldestTS != 100 {
		t.Errorf("newest/oldest = %d/%d, want 300/100", st.NewestTS, st.OldestTS)
	}
	if st.TagCounts["work"] != 2 || st.TagCounts["urgent"] != 1 {
		t.Errorf("tag counts = %v", st.TagCounts)
	}
}

func TestStats_EmptyStore(t *testing.T) {
	s := newTestStore(t)
	st, err := s.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.Total != 0 || st.Pinned != 0 || st.NewestTS != 0 || st.OldestTS != 0 {
		t.Errorf("empty stats not zeroed: %+v", st)
	}
	if len(st.TagCounts) != 0 {
		t.Errorf("expected no tags, got %v", st.TagCounts)
	}
}

func TestMetaAndLastSync(t *testing.T) {
	s := newTestStore(t)
	if v, _ := s.GetMeta("missing"); v != "" {
		t.Errorf("GetMeta(missing) = %q, want empty", v)
	}
	if err := s.SetMeta("k", "v1"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.GetMeta("k"); v != "v1" {
		t.Errorf("GetMeta after set = %q, want v1", v)
	}
	// upsert path
	_ = s.SetMeta("k", "v2")
	if v, _ := s.GetMeta("k"); v != "v2" {
		t.Errorf("GetMeta after re-set = %q, want v2", v)
	}

	if s.LastSyncUnix() != 0 {
		t.Error("LastSyncUnix should be 0 before first sync")
	}
	if err := s.SetLastSyncNow(); err != nil {
		t.Fatal(err)
	}
	if s.LastSyncUnix() == 0 {
		t.Error("LastSyncUnix should be non-zero after SetLastSyncNow")
	}
}

func TestSplitTags(t *testing.T) {
	cases := map[string][]string{
		"":          nil,
		"   ":       nil,
		"a":         {"a"},
		"a b c":     {"a", "b", "c"},
		"  a   b  ": {"a", "b"},
	}
	for in, want := range cases {
		got := splitTags(in)
		if len(got) != len(want) {
			t.Errorf("splitTags(%q) = %v, want %v", in, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("splitTags(%q) = %v, want %v", in, got, want)
				break
			}
		}
	}
}

func TestFTSTriggersKeepIndexConsistent(t *testing.T) {
	s := newTestStore(t)
	mustUpsert(t, s, &Memo{UID: "1", Content: "alpha content", Visibility: "PRIVATE"})
	// update should re-index; searching the old term must miss, new term must hit
	mustUpsert(t, s, &Memo{UID: "1", Content: "bravo content", Visibility: "PRIVATE"})

	if hits, _ := s.Search("alpha", 10); len(hits) != 0 {
		t.Errorf("stale FTS entry: 'alpha' still matches after update: %v", hits)
	}
	if hits, _ := s.Search("bravo", 10); len(hits) != 1 {
		t.Errorf("updated FTS entry: 'bravo' should match, got %v", hits)
	}

	// delete should remove from FTS too
	_ = s.Delete("1")
	if hits, _ := s.Search("bravo", 10); len(hits) != 0 {
		t.Errorf("FTS entry survived delete: %v", hits)
	}
}

// --- attachments ------------------------------------------------------------

func TestAttachmentUpsertGetList(t *testing.T) {
	s := newTestStore(t)
	a1 := &Attachment{
		UID: "att1", MemoUID: "m1", Filename: "photo.png", Type: "image/png",
		Size: 2048, LocalPath: "/tmp/att/att1/photo.png", CreatedTime: 500,
	}
	a2 := &Attachment{
		UID: "att2", MemoUID: "m1", Filename: "doc.pdf", Type: "application/pdf",
		Size: 4096, CreatedTime: 600,
	}
	a3 := &Attachment{UID: "att3", MemoUID: "m2", Filename: "x.txt", CreatedTime: 100}
	for _, a := range []*Attachment{a1, a2, a3} {
		if err := s.UpsertAttachment(a); err != nil {
			t.Fatalf("UpsertAttachment(%s): %v", a.UID, err)
		}
	}

	got, err := s.GetAttachment("att1")
	if err != nil || got == nil {
		t.Fatalf("GetAttachment: %v %v", got, err)
	}
	if got.Filename != "photo.png" || got.Type != "image/png" || got.Size != 2048 ||
		got.LocalPath != "/tmp/att/att1/photo.png" || got.MemoUID != "m1" {
		t.Errorf("GetAttachment mismatch: %+v", got)
	}

	// ListAttachments for a memo, newest-first (created_ts desc): att2 before att1.
	list, err := s.ListAttachments("m1")
	if err != nil {
		t.Fatalf("ListAttachments: %v", err)
	}
	if len(list) != 2 || list[0].UID != "att2" || list[1].UID != "att1" {
		t.Errorf("ListAttachments(m1) = %+v, want [att2, att1]", list)
	}

	// ListAttachments("") returns all.
	all, _ := s.ListAttachments("")
	if len(all) != 3 {
		t.Errorf("ListAttachments(all) len = %d, want 3", len(all))
	}
}

func TestAttachmentUpsertPreservesLocalPath(t *testing.T) {
	s := newTestStore(t)
	// First: downloaded, has a local path.
	_ = s.UpsertAttachment(&Attachment{UID: "a", Filename: "f", LocalPath: "/data/a/f"})
	// Metadata refresh (e.g. from a list) with no local path must NOT clobber it.
	_ = s.UpsertAttachment(&Attachment{UID: "a", Filename: "f", Type: "image/png"})
	got, _ := s.GetAttachment("a")
	if got.LocalPath != "/data/a/f" {
		t.Errorf("local_path clobbered on metadata refresh: %q", got.LocalPath)
	}
	if got.Type != "image/png" {
		t.Errorf("type not updated: %q", got.Type)
	}
	// A refresh WITH a new local path should overwrite.
	_ = s.UpsertAttachment(&Attachment{UID: "a", Filename: "f", LocalPath: "/data/a/new"})
	got, _ = s.GetAttachment("a")
	if got.LocalPath != "/data/a/new" {
		t.Errorf("local_path not updated: %q", got.LocalPath)
	}
}

func TestGetAttachmentMissing(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetAttachment("nope")
	if err != nil {
		t.Fatalf("GetAttachment err: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for missing attachment, got %+v", got)
	}
}
