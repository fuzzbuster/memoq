// Package syncer reconciles a complete remote Memos snapshot with the local
// SQLite store, using fingerprints to skip unchanged notes.
package syncer

import (
	"context"
	"time"

	"github.com/example/memoq/internal/memos"
	"github.com/example/memoq/internal/store"
)

// Result summarizes a sync run.
type Result struct {
	Added     int           `json:"added"`
	Updated   int           `json:"updated"`
	Skipped   int           `json:"skipped"`
	Deleted   int           `json:"deleted"`
	Preserved int           `json:"preserved"`
	Total     int           `json:"total_remote"`
	Duration  time.Duration `json:"-"`
}

// Syncer wires a client and store together.
type Syncer struct {
	client *memos.Client
	st     *store.Store
}

// New builds a Syncer.
func New(client *memos.Client, st *store.Store) *Syncer {
	return &Syncer{client: client, st: st}
}

// Sync pulls all remote memos and reconciles the local store. It is a full
// fetch + hash-diff: cheap because the diff work is local, and robust against
// missed incremental windows. Deletions are detected by comparing remote UIDs
// against local UIDs.
func (s *Syncer) Sync(ctx context.Context) (*Result, error) {
	start := time.Now()
	res := &Result{}

	baseline, err := s.st.MemoHashSnapshot()
	if err != nil {
		return nil, err
	}

	remote, err := s.client.ListAll(ctx, 100)
	if err != nil {
		return nil, err
	}
	res.Total = len(remote)

	cached := make([]*store.Memo, 0, len(remote))
	for _, rm := range remote {
		hash := rm.CacheMD5()
		cached = append(cached, toStoreMemo(rm, hash))
	}

	reconciled, err := s.st.ReconcileSnapshot(baseline, cached)
	if err != nil {
		return nil, err
	}
	res.Added = reconciled.Added
	res.Updated = reconciled.Updated
	res.Skipped = reconciled.Skipped
	res.Deleted = reconciled.Deleted
	res.Preserved = reconciled.Preserved
	res.Duration = time.Since(start)
	return res, nil
}

// MaybeAutoSync runs Sync only if the local cache is staler than ttlSeconds.
// ttlSeconds <= 0 disables auto-sync. It returns the result (nil if skipped).
func (s *Syncer) MaybeAutoSync(ctx context.Context, ttlSeconds int) (*Result, error) {
	if ttlSeconds <= 0 {
		return nil, nil
	}
	last := s.st.LastSyncUnix()
	if last > 0 && time.Now().Unix()-last < int64(ttlSeconds) {
		return nil, nil // cache still fresh
	}
	return s.Sync(ctx)
}

func toStoreMemo(rm *memos.Memo, hash string) *store.Memo {
	var created, updated int64
	if rm.CreateTime != nil {
		created = rm.CreateTime.Unix()
	}
	if rm.UpdateTime != nil {
		updated = rm.UpdateTime.Unix()
	}
	vis := rm.Visibility
	if vis == "" {
		vis = "PRIVATE"
	}
	return &store.Memo{
		UID:         rm.UIDValue(),
		Content:     rm.Content,
		Tags:        rm.TagList(),
		Visibility:  vis,
		Pinned:      rm.Pinned,
		CreatedTime: created,
		UpdatedTime: updated,
		ContentHash: hash,
	}
}
