// Package store is memoq's local persistence layer: a single SQLite database
// with an FTS5 virtual table for full-text search. It has no ORM; plain
// database/sql keeps the dependency surface and binary small.
package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO), ships with FTS5
)

// Memo is the local representation of a Memos note.
type Memo struct {
	UID         string   `json:"uid"`
	Content     string   `json:"content"`
	Tags        []string `json:"tags"`
	Visibility  string   `json:"visibility"`
	Pinned      bool     `json:"pinned"`
	CreatedTime int64    `json:"created_ts"` // Unix seconds
	UpdatedTime int64    `json:"updated_ts"` // Unix seconds
	ContentHash string   `json:"-"`          // fingerprint of cached fields, for dedup
}

// ReconcileResult summarizes a transactional remote snapshot reconciliation.
type ReconcileResult struct {
	Added     int
	Updated   int
	Skipped   int
	Deleted   int
	Preserved int
}

// SearchHit is a memo plus its FTS relevance rank (lower = more relevant).
type SearchHit struct {
	Memo
	Rank float64 `json:"rank"`
}

// Attachment is the local record of a Memos attachment (a.k.a. resource). The
// blob bytes themselves are never returned by the JSON API (the proto marks the
// content field INPUT_ONLY); they are fetched separately from the file server
// and, once downloaded, LocalPath points at the on-disk copy so a coding agent
// can Read the image/file directly.
type Attachment struct {
	UID          string `json:"uid"`           // attachment id, from name "attachments/{id}"
	MemoUID      string `json:"memo_uid"`      // owning memo uid (may be empty)
	Filename     string `json:"filename"`      // original file name
	Type         string `json:"type"`          // MIME type
	Size         int64  `json:"size"`          // bytes
	ExternalLink string `json:"external_link"` // set when hosted externally
	LocalPath    string `json:"local_path"`    // absolute path once downloaded ("" if not)
	CreatedTime  int64  `json:"created_ts"`    // Unix seconds
}

// Store wraps the SQLite connection.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path and runs
// migrations. WAL mode is enabled for concurrent read while syncing.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	// modernc driver: one connection avoids "database is locked" for a CLI.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// migrate creates tables, the FTS5 index, and triggers that keep FTS in sync
// with the base table automatically on insert/update/delete.
func (s *Store) migrate() error {
	stmts := []string{
		`PRAGMA journal_mode=WAL;`,
		`PRAGMA foreign_keys=ON;`,
		`CREATE TABLE IF NOT EXISTS memos (
			uid          TEXT PRIMARY KEY,
			content      TEXT NOT NULL,
			tags         TEXT NOT NULL DEFAULT '',  -- space-joined tags for FTS + LIKE
			visibility   TEXT NOT NULL DEFAULT 'PRIVATE',
			pinned       INTEGER NOT NULL DEFAULT 0,
			created_ts   INTEGER NOT NULL DEFAULT 0,
			updated_ts   INTEGER NOT NULL DEFAULT 0,
			content_hash TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE INDEX IF NOT EXISTS idx_memos_created ON memos(created_ts);`,
		`CREATE INDEX IF NOT EXISTS idx_memos_updated ON memos(updated_ts);`,
		// FTS5 external-content table indexing content + tags.
		// unicode61 with remove_diacritics keeps it simple and CJK-tolerant
		// (matches on substrings via the trigram-free default is limited for
		// CJK, so we also expose LIKE fallback in Search()).
		`CREATE VIRTUAL TABLE IF NOT EXISTS memos_fts USING fts5(
			content, tags,
			content='memos', content_rowid='rowid',
			tokenize='unicode61'
		);`,
		// Triggers keep the FTS index consistent with the base table.
		`CREATE TRIGGER IF NOT EXISTS memos_ai AFTER INSERT ON memos BEGIN
			INSERT INTO memos_fts(rowid, content, tags) VALUES (new.rowid, new.content, new.tags);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS memos_ad AFTER DELETE ON memos BEGIN
			INSERT INTO memos_fts(memos_fts, rowid, content, tags) VALUES('delete', old.rowid, old.content, old.tags);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS memos_au AFTER UPDATE ON memos BEGIN
			INSERT INTO memos_fts(memos_fts, rowid, content, tags) VALUES('delete', old.rowid, old.content, old.tags);
			INSERT INTO memos_fts(rowid, content, tags) VALUES (new.rowid, new.content, new.tags);
		END;`,
		// Simple key/value table for sync state (last sync time per server).
		`CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			val TEXT NOT NULL
		);`,
		// Attachment metadata cache. Blob bytes live on disk (local_path), not
		// here; this table lets the agent discover which files a memo has and
		// where the downloaded copy is.
		`CREATE TABLE IF NOT EXISTS attachments (
			uid           TEXT PRIMARY KEY,
			memo_uid      TEXT NOT NULL DEFAULT '',
			filename      TEXT NOT NULL DEFAULT '',
			type          TEXT NOT NULL DEFAULT '',
			size          INTEGER NOT NULL DEFAULT 0,
			external_link TEXT NOT NULL DEFAULT '',
			local_path    TEXT NOT NULL DEFAULT '',
			created_ts    INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE INDEX IF NOT EXISTS idx_attachments_memo ON attachments(memo_uid);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate failed on %q: %w", firstLine(stmt), err)
		}
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// --- meta helpers -----------------------------------------------------------

// GetMeta returns a meta value (empty string if absent).
func (s *Store) GetMeta(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT val FROM meta WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// SetMeta upserts a meta value.
func (s *Store) SetMeta(key, val string) error {
	_, err := s.db.Exec(
		`INSERT INTO meta(key,val) VALUES(?,?)
		 ON CONFLICT(key) DO UPDATE SET val=excluded.val`, key, val)
	return err
}

// LastSyncUnix returns the timestamp of the last successful sync (0 if never).
func (s *Store) LastSyncUnix() int64 {
	v, _ := s.GetMeta("last_sync_unix")
	if v == "" {
		return 0
	}
	var ts int64
	fmt.Sscanf(v, "%d", &ts)
	return ts
}

// SetLastSyncNow records the current time as the last sync time.
func (s *Store) SetLastSyncNow() error {
	return s.SetMeta("last_sync_unix", fmt.Sprintf("%d", time.Now().Unix()))
}

// --- attachments ------------------------------------------------------------

// UpsertAttachment inserts or updates an attachment record by UID. It preserves
// an existing local_path when the incoming record has none, so metadata refreshes
// (e.g. from a list) don't clobber a previously downloaded blob's path.
func (s *Store) UpsertAttachment(a *Attachment) error {
	_, err := s.db.Exec(`
		INSERT INTO attachments(uid,memo_uid,filename,type,size,external_link,local_path,created_ts)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(uid) DO UPDATE SET
			memo_uid=excluded.memo_uid,
			filename=excluded.filename,
			type=excluded.type,
			size=excluded.size,
			external_link=excluded.external_link,
			local_path=CASE WHEN excluded.local_path <> '' THEN excluded.local_path ELSE attachments.local_path END,
			created_ts=excluded.created_ts`,
		a.UID, a.MemoUID, a.Filename, a.Type, a.Size, a.ExternalLink, a.LocalPath, a.CreatedTime)
	return err
}

// GetAttachment returns a single attachment by UID (nil if not found).
func (s *Store) GetAttachment(uid string) (*Attachment, error) {
	row := s.db.QueryRow(`
		SELECT uid,memo_uid,filename,type,size,external_link,local_path,created_ts
		FROM attachments WHERE uid=?`, uid)
	a, err := scanAttachment(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return a, err
}

// ListAttachments returns attachments for a memo (memoUID=="" returns all),
// newest first.
func (s *Store) ListAttachments(memoUID string) ([]*Attachment, error) {
	q := `SELECT uid,memo_uid,filename,type,size,external_link,local_path,created_ts FROM attachments`
	var args []any
	if memoUID != "" {
		q += ` WHERE memo_uid=?`
		args = append(args, memoUID)
	}
	q += ` ORDER BY created_ts DESC, uid`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Attachment
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func scanAttachment(row scannable) (*Attachment, error) {
	var a Attachment
	if err := row.Scan(&a.UID, &a.MemoUID, &a.Filename, &a.Type, &a.Size,
		&a.ExternalLink, &a.LocalPath, &a.CreatedTime); err != nil {
		return nil, err
	}
	return &a, nil
}

// --- writes -----------------------------------------------------------------

// Upsert inserts or updates a memo by UID. Tags are stored space-joined so
// FTS and LIKE both work.
func (s *Store) Upsert(m *Memo) error {
	_, err := s.db.Exec(`
		INSERT INTO memos(uid,content,tags,visibility,pinned,created_ts,updated_ts,content_hash)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(uid) DO UPDATE SET
			content=excluded.content,
			tags=excluded.tags,
			visibility=excluded.visibility,
			pinned=excluded.pinned,
			created_ts=excluded.created_ts,
			updated_ts=excluded.updated_ts,
			content_hash=excluded.content_hash`,
		m.UID, m.Content, strings.Join(m.Tags, " "), m.Visibility, boolToInt(m.Pinned),
		m.CreatedTime, m.UpdatedTime, m.ContentHash)
	return err
}

// Delete removes a memo and its attachment metadata by UID.
func (s *Store) Delete(uid string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM attachments WHERE memo_uid=?`, uid); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM memos WHERE uid=?`, uid); err != nil {
		return err
	}
	return tx.Commit()
}

// MemoHashSnapshot returns the current UID-to-fingerprint map for CAS reconciliation.
func (s *Store) MemoHashSnapshot() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT uid, content_hash FROM memos`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	snapshot := make(map[string]string)
	for rows.Next() {
		var uid, hash string
		if err := rows.Scan(&uid, &hash); err != nil {
			return nil, err
		}
		snapshot[uid] = hash
	}
	return snapshot, rows.Err()
}

// ReconcileSnapshot applies a complete remote snapshot without overwriting
// local writes that occurred after baseline was captured.
func (s *Store) ReconcileSnapshot(baseline map[string]string, remote []*Memo) (*ReconcileResult, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	result := &ReconcileResult{}
	remoteUIDs := make(map[string]bool, len(remote))
	for _, memo := range remote {
		remoteUIDs[memo.UID] = true
		oldHash, existed := baseline[memo.UID]
		if existed && oldHash == memo.ContentHash {
			result.Skipped++
			continue
		}

		var sqlResult sql.Result
		if existed {
			sqlResult, err = tx.Exec(`
				UPDATE memos SET
					content=?, tags=?, visibility=?, pinned=?,
					created_ts=?, updated_ts=?, content_hash=?
				WHERE uid=? AND content_hash=?`,
				memo.Content, strings.Join(memo.Tags, " "), memo.Visibility, boolToInt(memo.Pinned),
				memo.CreatedTime, memo.UpdatedTime, memo.ContentHash, memo.UID, oldHash)
		} else {
			sqlResult, err = tx.Exec(`
				INSERT INTO memos(uid,content,tags,visibility,pinned,created_ts,updated_ts,content_hash)
				VALUES(?,?,?,?,?,?,?,?)
				ON CONFLICT(uid) DO NOTHING`,
				memo.UID, memo.Content, strings.Join(memo.Tags, " "), memo.Visibility,
				boolToInt(memo.Pinned), memo.CreatedTime, memo.UpdatedTime, memo.ContentHash)
		}
		if err != nil {
			return nil, err
		}
		affected, err := sqlResult.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected == 0 {
			result.Preserved++
		} else if existed {
			result.Updated++
		} else {
			result.Added++
		}
	}

	for uid, oldHash := range baseline {
		if remoteUIDs[uid] {
			continue
		}
		sqlResult, err := tx.Exec(`DELETE FROM memos WHERE uid=? AND content_hash=?`, uid, oldHash)
		if err != nil {
			return nil, err
		}
		affected, err := sqlResult.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected == 0 {
			result.Preserved++
			continue
		}
		if _, err := tx.Exec(`DELETE FROM attachments WHERE memo_uid=?`, uid); err != nil {
			return nil, err
		}
		result.Deleted++
	}

	if _, err := tx.Exec(
		`INSERT INTO meta(key,val) VALUES('last_sync_unix',?)
		 ON CONFLICT(key) DO UPDATE SET val=excluded.val`,
		fmt.Sprintf("%d", time.Now().Unix())); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

// --- reads ------------------------------------------------------------------

// ListFilter narrows a List query. Zero values mean "no constraint".
type ListFilter struct {
	Tag        string
	FromUnix   int64 // created_ts >= FromUnix
	ToUnix     int64 // created_ts <= ToUnix
	Visibility string
	Limit      int
	Offset     int
}

// Get returns a single memo by UID (nil if not found).
func (s *Store) Get(uid string) (*Memo, error) {
	row := s.db.QueryRow(`
		SELECT uid,content,tags,visibility,pinned,created_ts,updated_ts,content_hash
		FROM memos WHERE uid=?`, uid)
	m, err := scanMemo(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return m, err
}

// List returns memos matching the filter, newest first.
func (s *Store) List(f ListFilter) ([]*Memo, error) {
	var where []string
	var args []any
	if f.Tag != "" {
		where = append(where, `(' '||tags||' ') LIKE ?`)
		args = append(args, "% "+f.Tag+" %")
	}
	if f.FromUnix > 0 {
		where = append(where, `created_ts >= ?`)
		args = append(args, f.FromUnix)
	}
	if f.ToUnix > 0 {
		where = append(where, `created_ts <= ?`)
		args = append(args, f.ToUnix)
	}
	if f.Visibility != "" {
		where = append(where, `visibility = ?`)
		args = append(args, f.Visibility)
	}
	q := `SELECT uid,content,tags,visibility,pinned,created_ts,updated_ts,content_hash FROM memos`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY pinned DESC, created_ts DESC"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", f.Limit)
	}
	if f.Offset > 0 {
		q += fmt.Sprintf(" OFFSET %d", f.Offset)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemos(rows)
}

// Search runs a full-text query. It first tries FTS5 MATCH; if the query
// yields no hits (common for CJK substrings that FTS5 unicode61 won't tokenize
// well), it falls back to a LIKE scan so the agent still gets results.
func (s *Store) Search(query string, limit int) ([]*SearchHit, error) {
	if limit <= 0 {
		limit = 10
	}
	hits, err := s.searchFTS(query, limit)
	if err == nil && len(hits) > 0 {
		return hits, nil
	}
	// Fallback: substring LIKE across content + tags (handles CJK).
	return s.searchLike(query, limit)
}

func (s *Store) searchFTS(query string, limit int) ([]*SearchHit, error) {
	rows, err := s.db.Query(`
		SELECT m.uid,m.content,m.tags,m.visibility,m.pinned,m.created_ts,m.updated_ts,m.content_hash,
		       bm25(memos_fts) AS rank
		FROM memos_fts
		JOIN memos m ON m.rowid = memos_fts.rowid
		WHERE memos_fts MATCH ?
		ORDER BY rank
		LIMIT ?`, ftsEscape(query), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*SearchHit
	for rows.Next() {
		var h SearchHit
		var tags string
		var pinned int
		if err := rows.Scan(&h.UID, &h.Content, &tags, &h.Visibility, &pinned,
			&h.CreatedTime, &h.UpdatedTime, &h.ContentHash, &h.Rank); err != nil {
			return nil, err
		}
		h.Pinned = pinned == 1
		h.Tags = splitTags(tags)
		out = append(out, &h)
	}
	return out, rows.Err()
}

func (s *Store) searchLike(query string, limit int) ([]*SearchHit, error) {
	like := "%" + query + "%"
	rows, err := s.db.Query(`
		SELECT uid,content,tags,visibility,pinned,created_ts,updated_ts,content_hash
		FROM memos
		WHERE content LIKE ? OR tags LIKE ?
		ORDER BY pinned DESC, created_ts DESC
		LIMIT ?`, like, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*SearchHit
	for rows.Next() {
		var h SearchHit
		var tags string
		var pinned int
		if err := rows.Scan(&h.UID, &h.Content, &tags, &h.Visibility, &pinned,
			&h.CreatedTime, &h.UpdatedTime, &h.ContentHash); err != nil {
			return nil, err
		}
		h.Pinned = pinned == 1
		h.Tags = splitTags(tags)
		h.Rank = 0
		out = append(out, &h)
	}
	return out, rows.Err()
}

// Stats holds aggregate counts for the `stats` command.
type Stats struct {
	Total      int            `json:"total"`
	Pinned     int            `json:"pinned"`
	TagCounts  map[string]int `json:"tag_counts"`
	LastSyncTS int64          `json:"last_sync_ts"`
	NewestTS   int64          `json:"newest_ts"`
	OldestTS   int64          `json:"oldest_ts"`
}

// Stats computes an overview of the local cache.
func (s *Store) Stats() (*Stats, error) {
	st := &Stats{TagCounts: map[string]int{}}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM memos`).Scan(&st.Total); err != nil {
		return nil, err
	}
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM memos WHERE pinned=1`).Scan(&st.Pinned)
	_ = s.db.QueryRow(`SELECT COALESCE(MAX(created_ts),0), COALESCE(MIN(created_ts),0) FROM memos`).
		Scan(&st.NewestTS, &st.OldestTS)
	st.LastSyncTS = s.LastSyncUnix()

	rows, err := s.db.Query(`SELECT tags FROM memos WHERE tags <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var tags string
		if err := rows.Scan(&tags); err != nil {
			return nil, err
		}
		for _, t := range splitTags(tags) {
			st.TagCounts[t]++
		}
	}
	return st, rows.Err()
}

// --- scan helpers -----------------------------------------------------------

type scannable interface{ Scan(dest ...any) error }

func scanMemo(row scannable) (*Memo, error) {
	var m Memo
	var tags string
	var pinned int
	if err := row.Scan(&m.UID, &m.Content, &tags, &m.Visibility, &pinned,
		&m.CreatedTime, &m.UpdatedTime, &m.ContentHash); err != nil {
		return nil, err
	}
	m.Pinned = pinned == 1
	m.Tags = splitTags(tags)
	return &m, nil
}

func scanMemos(rows *sql.Rows) ([]*Memo, error) {
	var out []*Memo
	for rows.Next() {
		m, err := scanMemo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func splitTags(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ftsEscape wraps the query so arbitrary user text is treated as a phrase,
// avoiding FTS5 syntax errors on characters like ':' or '-'.
func ftsEscape(q string) string {
	q = strings.ReplaceAll(q, `"`, `""`)
	return `"` + q + `"`
}
