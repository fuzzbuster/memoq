package memos

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// capturedRequest records what the mock server saw, for assertions.
type capturedRequest struct {
	method string
	path   string // includes RawQuery
	auth   string
	accept string
	ctype  string
	body   string
}

// mockServer spins up an httptest server whose handler records the last request
// and replies with the supplied status/body. It returns a Client pointed at it
// plus a pointer to the captured request.
func mockServer(t *testing.T, status int, respBody string) (*Client, *capturedRequest) {
	t.Helper()
	cap := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.method = r.Method
		cap.path = r.URL.RequestURI()
		cap.auth = r.Header.Get("Authorization")
		cap.accept = r.Header.Get("Accept")
		cap.ctype = r.Header.Get("Content-Type")
		buf, _ := io.ReadAll(r.Body)
		cap.body = string(buf)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "test-token"), cap
}

func TestNew_TrimsTrailingSlash(t *testing.T) {
	c := New("https://memos.example.com/", "tok")
	if c.baseURL != "https://memos.example.com" {
		t.Errorf("baseURL = %q, want trailing slash trimmed", c.baseURL)
	}
}

func TestDo_SetsHeadersAndReturnsRawJSON(t *testing.T) {
	c, cap := mockServer(t, 200, `{"hello":"world"}`)
	raw, err := c.Do(context.Background(), http.MethodGet, "/api/v1/memos", nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if string(raw) != `{"hello":"world"}` {
		t.Errorf("raw = %s", raw)
	}
	if cap.auth != "Bearer test-token" {
		t.Errorf("Authorization = %q, want Bearer test-token", cap.auth)
	}
	if cap.accept != "application/json" {
		t.Errorf("Accept = %q", cap.accept)
	}
	if cap.method != "GET" || cap.path != "/api/v1/memos" {
		t.Errorf("request = %s %s", cap.method, cap.path)
	}
}

func TestDo_SendsBodyAndContentType(t *testing.T) {
	c, cap := mockServer(t, 200, `{}`)
	_, err := c.Do(context.Background(), http.MethodPost, "/api/v1/memos", map[string]any{"content": "hi"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if cap.ctype != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", cap.ctype)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(cap.body), &got); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, cap.body)
	}
	if got["content"] != "hi" {
		t.Errorf("body content = %v", got["content"])
	}
}

func TestDo_RawMessageBodySentVerbatim(t *testing.T) {
	c, cap := mockServer(t, 200, `{}`)
	_, err := c.Do(context.Background(), http.MethodPost, "/x", json.RawMessage(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(cap.body) != `{"a":1}` {
		t.Errorf("raw body = %q", cap.body)
	}
}

func TestDo_EmptyResponseBecomesEmptyObject(t *testing.T) {
	c, _ := mockServer(t, 204, "")
	raw, err := c.Do(context.Background(), http.MethodDelete, "/x", nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if string(raw) != `{}` {
		t.Errorf("empty 204 body = %s, want {}", raw)
	}
}

func TestDo_Non2xxIsError(t *testing.T) {
	c, _ := mockServer(t, 404, `{"error":"not found"}`)
	_, err := c.Do(context.Background(), http.MethodGet, "/x", nil)
	if err == nil {
		t.Fatal("expected error on 404")
	}
	if !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should include status and body: %v", err)
	}
}

func TestDo_NoTokenOmitsAuthHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("Authorization should be empty when no token set, got %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "")
	if _, err := c.Do(context.Background(), http.MethodGet, "/x", nil); err != nil {
		t.Fatal(err)
	}
}

func TestListAll_FollowsPagination(t *testing.T) {
	page := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page++
		switch r.URL.Query().Get("pageToken") {
		case "":
			_, _ = w.Write([]byte(`{"memos":[{"uid":"a"},{"uid":"b"}],"nextPageToken":"tok2"}`))
		case "tok2":
			_, _ = w.Write([]byte(`{"memos":[{"uid":"c"}],"nextPageToken":""}`))
		default:
			t.Errorf("unexpected pageToken %q", r.URL.Query().Get("pageToken"))
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "t")
	all, err := c.ListAll(context.Background(), 50)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d memos across pages, want 3", len(all))
	}
	if all[0].UID != "a" || all[2].UID != "c" {
		t.Errorf("unexpected memos: %+v", all)
	}
	if page != 2 {
		t.Errorf("server hit %d times, want 2 pages", page)
	}
}

func TestListAll_StopsOnEmptyPage(t *testing.T) {
	// A server that always returns a nextPageToken but eventually an empty memo
	// slice must not loop forever.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pageToken") == "" {
			_, _ = w.Write([]byte(`{"memos":[{"uid":"a"}],"nextPageToken":"more"}`))
			return
		}
		_, _ = w.Write([]byte(`{"memos":[],"nextPageToken":"more"}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "t")
	all, err := c.ListAll(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Errorf("got %d, want 1 (loop must stop on empty page)", len(all))
	}
}

func TestGet(t *testing.T) {
	c, cap := mockServer(t, 200, `{"uid":"xyz","content":"note"}`)
	m, err := c.Get(context.Background(), "xyz")
	if err != nil {
		t.Fatal(err)
	}
	if m.Content != "note" {
		t.Errorf("content = %q", m.Content)
	}
	if cap.path != "/api/v1/memos/xyz" {
		t.Errorf("path = %q", cap.path)
	}
}

func TestCreate_DefaultsVisibility(t *testing.T) {
	c, cap := mockServer(t, 200, `{"uid":"new","content":"body","visibility":"PRIVATE"}`)
	_, err := c.Create(context.Background(), "body", "")
	if err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	_ = json.Unmarshal([]byte(cap.body), &sent)
	if sent["visibility"] != "PRIVATE" {
		t.Errorf("visibility default = %v, want PRIVATE", sent["visibility"])
	}
	if sent["content"] != "body" {
		t.Errorf("content = %v", sent["content"])
	}
}

func TestUpdate_BuildsUpdateMask(t *testing.T) {
	cases := []struct {
		name       string
		content    string
		visibility string
		wantMask   string
		wantFields []string
	}{
		{"content only", "new", "", "content", []string{"content"}},
		{"visibility only", "", "PUBLIC", "visibility", []string{"visibility"}},
		{"both", "new", "PUBLIC", "content,visibility", []string{"content", "visibility"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, cap := mockServer(t, 200, `{"uid":"u"}`)
			_, err := c.Update(context.Background(), "u", tc.content, tc.visibility)
			if err != nil {
				t.Fatal(err)
			}
			if got := parseQuery(cap.path, "updateMask"); got != tc.wantMask {
				t.Errorf("updateMask = %q, want %q", got, tc.wantMask)
			}
			var patch map[string]any
			_ = json.Unmarshal([]byte(cap.body), &patch)
			for _, f := range tc.wantFields {
				if _, ok := patch[f]; !ok {
					t.Errorf("patch missing field %q (body=%s)", f, cap.body)
				}
			}
		})
	}
}

func TestDelete(t *testing.T) {
	c, cap := mockServer(t, 200, ``)
	if err := c.Delete(context.Background(), "gone"); err != nil {
		t.Fatal(err)
	}
	if cap.method != "DELETE" || cap.path != "/api/v1/memos/gone" {
		t.Errorf("request = %s %s", cap.method, cap.path)
	}
}

func TestPing_HitsAuthMe(t *testing.T) {
	c, cap := mockServer(t, 200, `{"name":"users/1"}`)
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Regression guard for the upstream drift fix: must be /auth/me, not /auth/status.
	if cap.path != "/api/v1/auth/me" {
		t.Errorf("Ping path = %q, want /api/v1/auth/me", cap.path)
	}
}

func TestPing_ErrorOnUnauthorized(t *testing.T) {
	c, _ := mockServer(t, 401, `{"error":"unauthorized"}`)
	if err := c.Ping(context.Background()); err == nil {
		t.Error("expected error on 401")
	}
}

// --- Memo struct helpers ----------------------------------------------------

func TestUIDValue(t *testing.T) {
	cases := []struct {
		memo Memo
		want string
	}{
		{Memo{UID: "direct"}, "direct"},
		{Memo{Name: "memos/frommname"}, "frommname"},
		{Memo{Name: "weird"}, "weird"},
		{Memo{UID: "wins", Name: "memos/loses"}, "wins"},
	}
	for _, tc := range cases {
		if got := tc.memo.UIDValue(); got != tc.want {
			t.Errorf("UIDValue(%+v) = %q, want %q", tc.memo, got, tc.want)
		}
	}
}

func TestTagList(t *testing.T) {
	// top-level tags win
	m := Memo{Tags: []string{"a", "b"}}
	if got := m.TagList(); len(got) != 2 {
		t.Errorf("TagList top-level = %v", got)
	}
	// fall back to property tags
	m2 := Memo{Property: &struct {
		Tags []string `json:"tags"`
	}{Tags: []string{"x"}}}
	if got := m2.TagList(); len(got) != 1 || got[0] != "x" {
		t.Errorf("TagList property fallback = %v", got)
	}
	// none
	empty := Memo{}
	if got := empty.TagList(); got != nil {
		t.Errorf("TagList empty = %v, want nil", got)
	}
}

func TestContentMD5_StableAndDistinct(t *testing.T) {
	a := Memo{Content: "same"}
	b := Memo{Content: "same"}
	c := Memo{Content: "different"}
	if a.ContentMD5() != b.ContentMD5() {
		t.Error("identical content must hash equally")
	}
	if a.ContentMD5() == c.ContentMD5() {
		t.Error("different content must hash differently")
	}
	if len(a.ContentMD5()) != 32 {
		t.Errorf("MD5 hex length = %d, want 32", len(a.ContentMD5()))
	}
}

func TestMemo_UnmarshalWithTimes(t *testing.T) {
	raw := `{"uid":"t","content":"c","createTime":"2026-01-02T03:04:05Z"}`
	var m Memo
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if m.CreateTime == nil || !m.CreateTime.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Errorf("createTime parsed as %v", m.CreateTime)
	}
}

// parseQuery extracts a single query param value from a "path?query" string.
func parseQuery(reqURI, key string) string {
	i := strings.IndexByte(reqURI, '?')
	if i < 0 {
		return ""
	}
	vals, _ := parseRawQuery(reqURI[i+1:])
	return vals[key]
}

// parseRawQuery is a tiny stand-in for url.ParseQuery that also URL-decodes
// commas so updateMask=content%2Cvisibility compares cleanly.
func parseRawQuery(raw string) (map[string]string, error) {
	out := map[string]string{}
	for _, pair := range strings.Split(raw, "&") {
		if pair == "" {
			continue
		}
		k, v, _ := strings.Cut(pair, "=")
		v = strings.ReplaceAll(v, "%2C", ",")
		v = strings.ReplaceAll(v, "%2c", ",")
		out[k] = v
	}
	return out, nil
}

// --- attachments ------------------------------------------------------------

func TestAttachmentIDAndMemoUID(t *testing.T) {
	a := &Attachment{Name: "attachments/42", Memo: "memos/xyz"}
	if a.IDValue() != "42" {
		t.Errorf("IDValue = %q, want 42", a.IDValue())
	}
	if a.MemoUID() != "xyz" {
		t.Errorf("MemoUID = %q, want xyz", a.MemoUID())
	}
	// Bare values (no prefix) pass through.
	b := &Attachment{Name: "99", Memo: "abc"}
	if b.IDValue() != "99" || b.MemoUID() != "abc" {
		t.Errorf("bare passthrough failed: %q %q", b.IDValue(), b.MemoUID())
	}
}

func TestListMemoAttachments(t *testing.T) {
	c, cap := mockServer(t, 200, `{"attachments":[
		{"name":"attachments/1","filename":"a.png","type":"image/png","size":"1234","memo":"memos/m1"},
		{"name":"attachments/2","filename":"b.pdf","type":"application/pdf","size":"5"}
	]}`)
	atts, err := c.ListMemoAttachments(context.Background(), "m1")
	if err != nil {
		t.Fatalf("ListMemoAttachments: %v", err)
	}
	if cap.method != http.MethodGet || cap.path != "/api/v1/memos/m1/attachments" {
		t.Errorf("request = %s %s, want GET /api/v1/memos/m1/attachments", cap.method, cap.path)
	}
	if len(atts) != 2 {
		t.Fatalf("got %d attachments, want 2", len(atts))
	}
	// size is sent as a string by the server; ",string" tag must parse it.
	if atts[0].Size != 1234 {
		t.Errorf("size = %d, want 1234", atts[0].Size)
	}
	if atts[0].IDValue() != "1" || atts[0].Filename != "a.png" {
		t.Errorf("attachment[0] = %+v", atts[0])
	}
}

func TestDownloadFile(t *testing.T) {
	blob := []byte("\x89PNG\r\n\x1a\n-fake-image-bytes")
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
		_, _ = w.Write(blob)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "test-token")

	var buf strings.Builder
	n, err := c.DownloadFile(context.Background(), "attachments/123", "photo.png", &buf)
	if err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	// Blobs come from the /file route, NOT /api/v1.
	if gotPath != "/file/attachments/123/photo.png" {
		t.Errorf("download path = %q, want /file/attachments/123/photo.png", gotPath)
	}
	// The bearer token must be attached (PRIVATE attachments need it).
	if gotAuth != "Bearer test-token" {
		t.Errorf("auth header = %q, want Bearer test-token", gotAuth)
	}
	if int(n) != len(blob) || buf.String() != string(blob) {
		t.Errorf("downloaded %d bytes, want %d; content mismatch", n, len(blob))
	}
}

func TestDownloadFileNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte("not found"))
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "tok")
	var buf strings.Builder
	if _, err := c.DownloadFile(context.Background(), "attachments/1", "x", &buf); err == nil {
		t.Fatal("expected error on 404, got nil")
	}
}
