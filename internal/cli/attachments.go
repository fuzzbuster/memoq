package cli

// Attachment blob download. Unlike the other `attachment` resource verbs (which
// just print server JSON), `attachment pull` bridges the JSON metadata API and
// the separate file-server route: it lists a memo's attachments, downloads each
// blob under $MEMOQ_HOME/attachments/<id>/<filename>, records the metadata plus
// the local path in the SQLite cache, and prints the absolute local paths so a
// coding agent can Read the image/file directly (e.g. to recognize an image).

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/example/memoq/internal/memos"
	"github.com/example/memoq/internal/store"
)

// cmdAttachmentPull implements:
//
//	memoq attachment pull <memo-uid> [--json] [--force]
//
// It downloads every attachment of the memo to the local attachments dir and
// prints where each one landed. --force re-downloads even if a local copy
// already exists.
func cmdAttachmentPull(args []string) error {
	fs := flag.NewFlagSet("attachment pull", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	force := fs.Bool("force", false, "re-download even if a local copy exists")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: memoq attachment pull <memo-uid> [--json] [--force]")
	}
	memoUID := pos[0]

	a, err := openApp()
	if err != nil {
		return err
	}
	defer a.close()
	cl, err := a.client()
	if err != nil {
		return err
	}
	if err := a.paths.EnsureAttachmentsDir(); err != nil {
		return err
	}

	ctx := context.Background()
	remote, err := cl.ListMemoAttachments(ctx, memoUID)
	if err != nil {
		return err
	}

	type pulled struct {
		UID       string `json:"uid"`
		Filename  string `json:"filename"`
		Type      string `json:"type"`
		Size      int64  `json:"size"`
		LocalPath string `json:"local_path"`
		Skipped   bool   `json:"skipped"`
	}
	var results []pulled

	for _, ra := range remote {
		id := ra.IDValue()
		owner := ra.MemoUID()
		if owner == "" {
			owner = memoUID
		}
		rec := &store.Attachment{
			UID:          id,
			MemoUID:      owner,
			Filename:     ra.Filename,
			Type:         ra.Type,
			Size:         ra.Size,
			ExternalLink: ra.ExternalLink,
			CreatedTime:  nowOr(ra.CreateTime),
		}
		// Externally-hosted attachments have no server blob to fetch.
		if ra.ExternalLink != "" {
			if err := a.store.UpsertAttachment(rec); err != nil {
				return err
			}
			results = append(results, pulled{id, ra.Filename, ra.Type, ra.Size, "", true})
			continue
		}

		dest, skipped, derr := downloadAttachment(ctx, cl, a.paths.AttachmentsDir, ra, *force)
		if derr != nil {
			return fmt.Errorf("download attachment %s (%s): %w", id, ra.Filename, derr)
		}
		rec.LocalPath = dest
		if err := a.store.UpsertAttachment(rec); err != nil {
			return err
		}
		results = append(results, pulled{id, ra.Filename, ra.Type, ra.Size, dest, skipped})
	}

	if *asJSON {
		return printJSON(results)
	}
	if len(results) == 0 {
		fmt.Printf("(memo %s has no attachments)\n", memoUID)
		return nil
	}
	for _, r := range results {
		switch {
		case r.LocalPath == "" && r.Skipped:
			fmt.Printf("- %s  (external link, not downloaded)\n", r.Filename)
		case r.Skipped:
			fmt.Printf("= %s  %s\n", r.Filename, r.LocalPath)
		default:
			fmt.Printf("+ %s  %s\n", r.Filename, r.LocalPath)
		}
	}
	return nil
}

// downloadAttachment writes one attachment blob to
// <dir>/<id>/<filename> and returns the absolute path. If a non-empty file
// already exists and force is false, it is left as-is (skipped=true).
func downloadAttachment(ctx context.Context, cl *memos.Client, dir string, ra *memos.Attachment, force bool) (string, bool, error) {
	id := ra.IDValue()
	fname := filepath.Base(ra.Filename) // guard against path traversal in the name
	if fname == "" || fname == "." || fname == string(os.PathSeparator) {
		fname = id
	}
	sub := filepath.Join(dir, id)
	if err := os.MkdirAll(sub, 0o755); err != nil {
		return "", false, err
	}
	dest := filepath.Join(sub, fname)
	abs, err := filepath.Abs(dest)
	if err != nil {
		abs = dest
	}

	if !force {
		if fi, statErr := os.Stat(abs); statErr == nil && fi.Size() > 0 {
			return abs, true, nil
		}
	}

	// Download to a temp file first, then rename, so a failed transfer never
	// leaves a truncated blob at the final path.
	tmp, err := os.CreateTemp(sub, ".dl-*")
	if err != nil {
		return "", false, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	if _, err := cl.DownloadFile(ctx, ra.Name, ra.Filename, tmp); err != nil {
		tmp.Close()
		return "", false, err
	}
	if err := tmp.Close(); err != nil {
		return "", false, err
	}
	if err := os.Rename(tmpName, abs); err != nil {
		return "", false, err
	}
	return abs, false, nil
}
