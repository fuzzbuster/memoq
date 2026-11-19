package memos

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUploadAttachmentFileChunks(t *testing.T) {
	for _, content := range []string{"", "abcdefg"} {
		t.Run(fmt.Sprintf("size%d", len(content)), func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "file.unknown")
			if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			var received []byte
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.URL.Path != "/api/attachments:upload" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				var body struct {
					Spec struct {
						TotalSize  string     `json:"totalSize"`
						Attachment Attachment `json:"attachment"`
					} `json:"spec"`
					UploadID string `json:"uploadId"`
					Offset   int64  `json:"writeOffset,string"`
					Data     []byte `json:"data"`
					Finish   bool   `json:"finishWrite"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				response := map[string]any{"uploadId": "id", "committedSize": fmt.Sprint(len(received)), "maxChunkSize": 3}
				if calls == 1 {
					if body.Spec.TotalSize != fmt.Sprint(len(content)) || body.Spec.Attachment.Memo != "memos/m" ||
						body.Spec.Attachment.Type != "application/octet-stream" {
						t.Errorf("wrong initialization: %#v", body.Spec)
					}
				} else {
					if body.UploadID != "id" || body.Offset != int64(len(received)) || len(body.Data) > 3 {
						t.Errorf("wrong chunk: %#v", body)
					}
					received = append(received, body.Data...)
					response["committedSize"] = fmt.Sprint(len(received))
					if body.Finish {
						if string(received) != content {
							t.Errorf("uploaded %q, want %q", received, content)
						}
						response["attachment"] = map[string]any{"name": "attachments/a", "size": fmt.Sprint(len(received))}
					}
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer srv.Close()
			a, err := New(srv.URL, "t").UploadAttachmentFile(context.Background(), file, "m", "")
			if err != nil || a == nil || a.Name != "attachments/a" || string(received) != content {
				t.Fatalf("attachment = %#v, err = %v, received = %q", a, err, received)
			}
			if calls != 1+max(1, (len(content)+2)/3) {
				t.Fatalf("upload calls = %d", calls)
			}
		})
	}
}

func TestUploadRejectsInconsistentProgress(t *testing.T) {
	for _, response := range []string{
		`{"uploadId":"other","committedSize":"1"}`,
		`{"uploadId":"id","committedSize":"0"}`,
		`{"uploadId":"id","committedSize":"1"}`,
	} {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 1 {
				fmt.Fprint(w, `{"uploadId":"id","committedSize":"0","maxChunkSize":1}`)
			} else {
				fmt.Fprint(w, response)
			}
		}))
		file := filepath.Join(t.TempDir(), "f")
		_ = os.WriteFile(file, []byte("x"), 0o600)
		_, err := New(srv.URL, "").UploadAttachmentFile(context.Background(), file, "", "")
		srv.Close()
		if err == nil {
			t.Fatalf("invalid final response accepted: %s", response)
		}
	}
}

func TestReplacementVersionGuard(t *testing.T) {
	for _, version := range []string{"26.10.0", "26.11.0", "26.11.1-rc.1", "unknown"} {
		uploads := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/instance/profile" {
				_ = json.NewEncoder(w).Encode(map[string]string{"version": version})
			} else {
				uploads++
			}
		}))
		_, err := New(srv.URL, "").UploadAttachmentFile(context.Background(), "unused", "", "a")
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), "26.11.1") || uploads != 0 {
			t.Fatalf("version %s: uploads=%d, err=%v", version, uploads, err)
		}
	}
}

func TestImportFinishingFieldsAndResult(t *testing.T) {
	file := filepath.Join(t.TempDir(), "archive")
	_ = os.WriteFile(file, []byte("zip"), 0o600)
	for _, validate := range []bool{true, false} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/users/alice:importMemos" {
				t.Errorf("wrong import path %s", r.URL.Path)
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			response := map[string]any{"uploadId": "id", "committedSize": "0", "maxChunkSize": 3}
			if body["finishWrite"] == true {
				if body["conflictPolicy"] != "DUPLICATE" || body["validateOnly"] != validate {
					t.Errorf("wrong finishing fields: %#v", body)
				}
				response["committedSize"] = "3"
				if validate {
					response["plan"] = map[string]int{"memoCount": 1}
				} else {
					response["report"] = map[string]int{"created": 1}
				}
			}
			_ = json.NewEncoder(w).Encode(response)
		}))
		raw, err := New(srv.URL, "").ImportMemosFile(context.Background(), "users/alice", file, "DUPLICATE", validate)
		srv.Close()
		if err != nil || len(raw) == 0 {
			t.Fatalf("import result: %s, %v", raw, err)
		}
	}
}
