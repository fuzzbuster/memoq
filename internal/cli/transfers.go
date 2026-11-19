package cli

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/example/memoq/internal/syncer"
)

func runAPIOutput(path string, af *apiFlags) error {
	if af.output != "" && af.stream {
		return fmt.Errorf("--output and --stream cannot be combined")
	}
	a, err := openApp()
	if err != nil {
		return err
	}
	defer a.close()
	cl, err := a.client()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if af.stream {
		return cl.Stream(ctx, path, os.Stdout)
	}
	dir := filepath.Dir(af.output)
	f, err := os.CreateTemp(dir, ".memoq-download-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	n, err := cl.Download(ctx, path, f)
	if err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), af.output); err != nil {
		return err
	}
	absolute, err := filepath.Abs(af.output)
	if err != nil {
		return err
	}
	return printJSON(map[string]any{"local_path": absolute, "size": n})
}

func runFileUpload(resource, name, path, file, memo, replace, policy string, validateOnly bool, af *apiFlags) error {
	if af.hasBodyInput() {
		return fmt.Errorf("--file cannot be combined with --body, --body-file, or --field")
	}
	if memo != "" && replace != "" {
		return fmt.Errorf("--memo cannot be combined with --replace")
	}
	fi, err := os.Stat(file)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("upload requires a regular file")
	}
	if af.dryRun {
		return printJSON(dryRunResult{
			DryRun: true, Operation: name, Resource: resource, Method: methodPost,
			Path: path, Body: map[string]any{
				"file": file, "size": fi.Size(), "memo": memo,
				"replace_attachment": replace, "conflict_policy": policy, "validate_only": validateOnly,
			},
			CachePolicy: cachePolicy(af.sync || resource == "user" && !validateOnly),
		})
	}
	if (replace != "" || resource == "user" && !validateOnly) && !af.yes {
		return confirmationRequired(resource + " " + name)
	}
	a, err := openApp()
	if err != nil {
		return err
	}
	defer a.close()
	cl, err := a.client()
	if err != nil {
		return err
	}
	if resource == "attachment" {
		attachment, err := cl.UploadAttachmentFile(context.Background(), file, memo, replace)
		if err != nil {
			return err
		}
		if af.sync {
			if _, err := syncer.New(cl, a.store).Sync(context.Background()); err != nil {
				warnCache(err)
			}
		}
		return printJSON(attachment)
	}
	user, err := url.PathUnescape(path[len("/api/users/") : len(path)-len(":importMemos")])
	if err != nil {
		return err
	}
	raw, err := cl.ImportMemosFile(context.Background(), user, file, policy, validateOnly)
	if err != nil {
		return err
	}
	if !validateOnly {
		if _, err := syncer.New(cl, a.store).Sync(context.Background()); err != nil {
			warnCache(err)
		}
	}
	return printRawJSON(raw)
}
