package memos

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// UploadAttachmentFile sends bounded chunks, including files larger than the
// inline JSON request limit. replace requires Memos 26.11.1 or later.
func (c *Client) UploadAttachmentFile(ctx context.Context, path, memo, replace string) (*Attachment, error) {
	if memo != "" && replace != "" {
		return nil, fmt.Errorf("memo and replacement attachment cannot be combined")
	}
	if replace != "" {
		var profile struct {
			Version string `json:"version"`
		}
		if err := c.do(ctx, methodGet, "/api/instance/profile", nil, &profile); err != nil {
			return nil, err
		}
		version, suffix, _ := strings.Cut(strings.TrimPrefix(profile.Version, "v"), "-")
		parts := strings.Split(version, ".")
		supported := false
		if len(parts) == 3 {
			major, e1 := strconv.Atoi(parts[0])
			minor, e2 := strconv.Atoi(parts[1])
			patch, e3 := strconv.Atoi(parts[2])
			supported = e1 == nil && e2 == nil && e3 == nil &&
				(major > 26 || major == 26 && (minor > 11 || minor == 11 && (patch > 1 || patch == 1 && suffix == "")))
		}
		if !supported {
			return nil, fmt.Errorf("attachment replacement requires Memos 26.11.1 or later (server: %q)", profile.Version)
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	attachment := map[string]any{
		"filename": filepath.Base(path),
		"type":     mime.TypeByExtension(filepath.Ext(path)),
	}
	if attachment["type"] == "" {
		attachment["type"] = "application/octet-stream"
	}
	if memo != "" {
		attachment["memo"] = resourceName("memos", memo)
	}
	spec := map[string]any{
		"attachment": attachment,
		"totalSize":  fmt.Sprint(fi.Size()),
	}
	if replace != "" {
		spec["replaceAttachment"] = resourceName("attachments", replace)
	}
	raw, err := c.uploadChunks(ctx, "/api/attachments:upload", f, fi.Size(), spec, nil)
	if err != nil {
		return nil, err
	}
	var response struct {
		Attachment *Attachment `json:"attachment"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	if response.Attachment == nil {
		return nil, fmt.Errorf("finished upload returned no attachment")
	}
	return response.Attachment, nil
}

// ImportMemosFile uploads an export archive and returns its plan or import report.
func (c *Client) ImportMemosFile(ctx context.Context, user, path, policy string, validateOnly bool) (json.RawMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	raw, err := c.uploadChunks(ctx, "/api/users/"+url.PathEscape(strings.TrimPrefix(user, "users/"))+":importMemos",
		f, fi.Size(), map[string]any{"totalSize": fmt.Sprint(fi.Size())},
		map[string]any{"conflictPolicy": policy, "validateOnly": validateOnly})
	if err != nil {
		return nil, err
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	field := "report"
	if validateOnly {
		field = "plan"
	}
	if len(result[field]) == 0 || string(result[field]) == "null" {
		return nil, fmt.Errorf("finished import returned no %s", field)
	}
	return raw, nil
}

func (c *Client) uploadChunks(ctx context.Context, path string, r io.Reader, size int64, spec any, finishFields map[string]any) (json.RawMessage, error) {
	type progress struct {
		UploadID      string `json:"uploadId"`
		CommittedSize int64  `json:"committedSize,string"`
		MaxChunkSize  int    `json:"maxChunkSize"`
	}
	var current progress
	if err := c.do(ctx, methodPost, path, map[string]any{"spec": spec}, &current); err != nil {
		return nil, err
	}
	if current.UploadID == "" || current.CommittedSize != 0 || current.MaxChunkSize <= 0 {
		return nil, fmt.Errorf("invalid upload initialization response")
	}
	buf := make([]byte, min(current.MaxChunkSize, 1<<20))
	for offset := int64(0); ; {
		n := int(min(int64(len(buf)), size-offset))
		if _, err := io.ReadFull(r, buf[:n]); err != nil {
			return nil, fmt.Errorf("read upload %s at offset %d: %w", current.UploadID, offset, err)
		}
		finish := offset+int64(n) == size
		body := map[string]any{
			"uploadId":    current.UploadID,
			"writeOffset": fmt.Sprint(offset),
			"data":        buf[:n],
			"finishWrite": finish,
		}
		if finish {
			for key, value := range finishFields {
				body[key] = value
			}
		}
		raw, err := c.Do(ctx, methodPost, path, body)
		if err != nil {
			return nil, fmt.Errorf("upload %s at offset %d: %w", current.UploadID, offset, err)
		}
		var next progress
		if err := json.Unmarshal(raw, &next); err != nil {
			return nil, fmt.Errorf("decode upload progress: %w", err)
		}
		offset += int64(n)
		if next.UploadID != current.UploadID || next.CommittedSize != offset {
			return nil, fmt.Errorf("upload %s returned inconsistent progress", current.UploadID)
		}
		if finish {
			return raw, nil
		}
	}
}
