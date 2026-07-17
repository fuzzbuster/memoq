// Package syncer performs incremental synchronization between a remote Memos
// server and the local SQLite store. It uses content MD5 hashes to skip
// unchanged notes and reconciles deletions, mirroring the upstream project's
// approach but without vectorization.
package syncer

import (
	"context"
	"time"

	"github.com/example/memoq/internal/memos"
	"github.com/example/memoq/internal/store"
)

// Result summarizes a sync run.
type Result struct {
	Added    int           `json:"added"`
	Updated  int           `json:"updated"`
	Skipped  int           `json:"skipped"`
	Deleted  int           `json:"deleted"`
	Total    int           `json:"total_remote"`
	Duration time.Duration `json:"-"`
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

	remote, err := s.client.ListAll(ctx, 100)
	if err != nil {
		return nil, err
	}
	res.Total = len(remote)

	remoteUIDs := make(map[string]bool, len(remote))
	for _, rm := range remote {
		uid := rm.UIDValue()
		if uid == "" {
			continue
		}
		remoteUIDs[uid] = true

		hash := rm.ContentMD5()
		oldHash, exists, err := s.st.HashByUID(uid)
		if err != nil {
			return nil, err
		}
		if exists && oldHash == hash {
			res.Skipped++
			continue
		}

		m := toStoreMemo(rm, hash)
		if err := s.st.Upsert(m); err != nil {
			return nil, err
		}
		if exists {
			res.Updated++
		} else {
			res.Added++
		}
	}

	// Reconcile deletions: anything local but not remote is gone.
	localUIDs, err := s.st.AllUIDs()
	if err != nil {
		return nil, err
	}
	for uid := range localUIDs {
		if !remoteUIDs[uid] {
			if err := s.st.Delete(uid); err != nil {
				return nil, err
			}
			res.Deleted++
		}
	}

	if err := s.st.SetLastSyncNow(); err != nil {
		return nil, err
	}
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
