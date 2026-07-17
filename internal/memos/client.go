// Package memos is a Memos v1 REST API client. It targets the Memos API used by
// the upstream project (github.com/usememos/memos) and aims to cover the full v1
// surface: memos, attachments, users, auth, shortcuts, instance settings, and AI
// transcription.
package memos

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/imroc/req/v3"
)

const (
	methodGet    = "GET"
	methodPost   = "POST"
	methodPatch  = "PATCH"
	methodDelete = "DELETE"
)

// Client talks to a single Memos server.
type Client struct {
	baseURL      string
	token        string
	http         *req.Client
	downloadHTTP *req.Client
}

// New builds a client. baseURL is the server root (without /api/v1).
func New(baseURL, token string) *Client {
	return &Client{
		baseURL:      strings.TrimRight(baseURL, "/"),
		token:        token,
		http:         newHTTPClient(false),
		downloadHTTP: newHTTPClient(true),
	}
}

func newHTTPClient(skipResponseBodyDump bool) *req.Client {
	c := req.C().SetTimeout(60 * time.Second)
	configureHTTPDebug(c, skipResponseBodyDump)
	return c
}

func configureHTTPDebug(c *req.Client, skipResponseBodyDump bool) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("MEMOQ_HTTP_DEBUG")))
	if mode == "" {
		return
	}

	dumpOpt := &req.DumpOptions{Output: os.Stderr}
	switch mode {
	case "headers":
		dumpOpt.RequestHeader = true
		dumpOpt.ResponseHeader = true
		c.SetCommonDumpOptions(dumpOpt).EnableDumpAll()
	case "dump":
		dumpOpt.RequestHeader = true
		dumpOpt.RequestBody = true
		dumpOpt.ResponseHeader = true
		dumpOpt.ResponseBody = !skipResponseBodyDump
		c.SetCommonDumpOptions(dumpOpt).EnableDumpAll()
	case "trace":
		c.EnableTraceAll().OnAfterResponse(func(_ *req.Client, resp *req.Response) error {
			fmt.Fprintf(os.Stderr, "%s\n%s\n", resp.TraceInfo().Blame(), resp.TraceInfo())
			return nil
		})
	case "debug":
		c.SetLogger(req.NewLogger(os.Stderr, "", 0)).EnableDebugLog()
	case "dev":
		dumpOpt.RequestHeader = true
		dumpOpt.RequestBody = true
		dumpOpt.ResponseHeader = true
		dumpOpt.ResponseBody = !skipResponseBodyDump
		c.SetCommonDumpOptions(dumpOpt).
			SetLogger(req.NewLogger(os.Stderr, "", 0)).
			DevMode()
	}
}

// Do performs an arbitrary authenticated request against a path under the server
// root (e.g. "/api/v1/memos") and returns the raw JSON response body. body may
// be nil (no request body), a []byte / json.RawMessage (sent verbatim), or any
// value that will be JSON-marshalled. This is the generic primitive the CLI
// resource commands are built on, guaranteeing full API coverage.
func (c *Client) Do(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var out json.RawMessage
	if err := c.do(ctx, method, path, body, &rawSink{&out}); err != nil {
		return nil, err
	}
	return out, nil
}

// rawSink lets do() capture the response body verbatim into a json.RawMessage
// while still going through the shared decode path.
type rawSink struct{ dst *json.RawMessage }

// do performs an HTTP request with auth, an optional JSON body, and decodes the
// response into out (if non-nil). It returns a descriptive error for non-2xx
// responses. out may be a *rawSink to capture the raw JSON body.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reqBody []byte
	if body != nil {
		switch b := body.(type) {
		case []byte:
			reqBody = b
		case json.RawMessage:
			reqBody = b
		default:
			buf, err := json.Marshal(body)
			if err != nil {
				return err
			}
			reqBody = buf
		}
	}

	req := c.http.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json")
	if body != nil {
		req.SetHeader("Content-Type", "application/json").
			SetBodyBytes(reqBody)
	}
	if c.token != "" {
		req.SetBearerAuthToken(c.token)
	}

	resp, err := req.Send(method, c.baseURL+path)
	if err != nil {
		return fmt.Errorf("request %s %s: %w", method, path, err)
	}
	data, _ := resp.ToBytes()

	if resp.GetStatusCode() < 200 || resp.GetStatusCode() >= 300 {
		return fmt.Errorf("memos API %s %s -> %d: %s",
			method, path, resp.GetStatusCode(), strings.TrimSpace(string(data)))
	}
	switch sink := out.(type) {
	case nil:
		return nil
	case *rawSink:
		// Ensure a valid JSON document even when the server returns an empty
		// body (e.g. 204 on delete).
		if len(bytes.TrimSpace(data)) == 0 {
			*sink.dst = json.RawMessage(`{}`)
		} else {
			*sink.dst = json.RawMessage(data)
		}
		return nil
	default:
		if len(data) > 0 {
			if err := json.Unmarshal(data, out); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
		}
		return nil
	}
}

// query builds an encoded query string ("?a=b&c=d") from key/value pairs,
// skipping pairs whose value is empty. Returns "" when nothing is set.
func query(pairs ...[2]string) string {
	q := url.Values{}
	for _, p := range pairs {
		if p[1] != "" {
			q.Set(p[0], p[1])
		}
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}

// Memo mirrors the subset of the Memos API resource the syncer/store care about.
type Memo struct {
	Name       string     `json:"name"` // "memos/{uid}"
	UID        string     `json:"uid"`  // some versions expose uid directly
	Content    string     `json:"content"`
	Visibility string     `json:"visibility"`
	Pinned     bool       `json:"pinned"`
	CreateTime *time.Time `json:"createTime"`
	UpdateTime *time.Time `json:"updateTime"`
	Tags       []string   `json:"tags"`
	Property   *struct {
		Tags []string `json:"tags"`
	} `json:"property"`
}

// UIDValue returns the memo's UID, extracting it from Name ("memos/xxx") if the
// dedicated uid field is empty.
func (m *Memo) UIDValue() string {
	if m.UID != "" {
		return m.UID
	}
	if strings.HasPrefix(m.Name, "memos/") {
		return strings.TrimPrefix(m.Name, "memos/")
	}
	return m.Name
}

// TagList returns the memo tags (never nil), preferring the top-level tags field
// and falling back to the computed property.
func (m *Memo) TagList() []string {
	if len(m.Tags) > 0 {
		return m.Tags
	}
	if m.Property != nil {
		return m.Property.Tags
	}
	return nil
}

// ContentMD5 returns the hex MD5 of the memo content, used for change dedup.
func (m *Memo) ContentMD5() string {
	sum := md5.Sum([]byte(m.Content))
	return hex.EncodeToString(sum[:])
}

// --- typed MemoService helpers ----------------------------------------------
//
// These convenience methods are what the syncer and the local-cache write
// commands are built on. Everything else in the CLI goes through the generic
// Do() primitive, but the hot sync path benefits from typed structs.

// listMemosResponse is the paginated envelope returned by GET /api/v1/memos.
type listMemosResponse struct {
	Memos         []*Memo `json:"memos"`
	NextPageToken string  `json:"nextPageToken"`
}

// ListAll fetches every memo the token can see, following pagination until the
// server stops returning a nextPageToken. pageSize bounds each page (the server
// may clamp it); 0 lets the server choose.
func (c *Client) ListAll(ctx context.Context, pageSize int) ([]*Memo, error) {
	var all []*Memo
	pageToken := ""
	for {
		ps := ""
		if pageSize > 0 {
			ps = fmt.Sprintf("%d", pageSize)
		}
		path := "/api/v1/memos" + query(
			[2]string{"pageSize", ps},
			[2]string{"pageToken", pageToken},
		)
		var resp listMemosResponse
		if err := c.do(ctx, methodGet, path, nil, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Memos...)
		if resp.NextPageToken == "" || len(resp.Memos) == 0 {
			break
		}
		pageToken = resp.NextPageToken
	}
	return all, nil
}

// Get fetches a single memo by UID.
func (c *Client) Get(ctx context.Context, uid string) (*Memo, error) {
	var m Memo
	if err := c.do(ctx, methodGet, "/api/v1/memos/"+url.PathEscape(uid), nil, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Create creates a memo with the given content and visibility (defaults to
// PRIVATE when empty) and returns the server's canonical representation.
func (c *Client) Create(ctx context.Context, content, visibility string) (*Memo, error) {
	if visibility == "" {
		visibility = "PRIVATE"
	}
	reqBody := map[string]any{"content": content, "visibility": visibility}
	var m Memo
	if err := c.do(ctx, methodPost, "/api/v1/memos", reqBody, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Update patches a memo's content and/or visibility. Empty fields are left
// unchanged; the updateMask is built from whichever fields are provided.
func (c *Client) Update(ctx context.Context, uid, content, visibility string) (*Memo, error) {
	patch := map[string]any{}
	var mask []string
	if content != "" {
		patch["content"] = content
		mask = append(mask, "content")
	}
	if visibility != "" {
		patch["visibility"] = visibility
		mask = append(mask, "visibility")
	}
	path := "/api/v1/memos/" + url.PathEscape(uid) + query(
		[2]string{"updateMask", strings.Join(mask, ",")},
	)
	var m Memo
	if err := c.do(ctx, methodPatch, path, patch, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Delete removes a memo by UID.
func (c *Client) Delete(ctx context.Context, uid string) error {
	return c.do(ctx, methodDelete, "/api/v1/memos/"+url.PathEscape(uid), nil, nil)
}

// Ping verifies connectivity and auth by fetching the authenticated user. It
// returns a descriptive error if the server is unreachable or the token is
// rejected.
func (c *Client) Ping(ctx context.Context) error {
	return c.do(ctx, methodGet, "/api/v1/auth/me", nil, nil)
}

// --- attachments ------------------------------------------------------------
//
// Attachment blobs are NOT delivered by the JSON API: the proto marks the
// `content` field INPUT_ONLY, so listing attachments only yields metadata. The
// raw bytes are served by a separate file-server route,
// `/file/{name}/{filename}` (e.g. /file/attachments/123/photo.png). PRIVATE and
// PROTECTED attachments require the bearer token; PUBLIC ones ignore it. The
// client always sends the token when present, so all three work.

// Attachment mirrors the metadata subset of a Memos attachment resource.
type Attachment struct {
	Name         string     `json:"name"`         // "attachments/{id}"
	Filename     string     `json:"filename"`     // original file name
	Type         string     `json:"type"`         // MIME type
	Size         int64      `json:"size,string"`  // bytes (server sends string)
	Memo         string     `json:"memo"`         // owning memo, "memos/{uid}" (optional)
	ExternalLink string     `json:"externalLink"` // set when hosted off-server
	CreateTime   *time.Time `json:"createTime"`
}

// IDValue returns the attachment id, extracted from Name ("attachments/{id}").
func (a *Attachment) IDValue() string {
	if strings.HasPrefix(a.Name, "attachments/") {
		return strings.TrimPrefix(a.Name, "attachments/")
	}
	return a.Name
}

// MemoUID returns the owning memo's uid, extracted from Memo ("memos/{uid}").
func (a *Attachment) MemoUID() string {
	if strings.HasPrefix(a.Memo, "memos/") {
		return strings.TrimPrefix(a.Memo, "memos/")
	}
	return a.Memo
}

// listAttachmentsResponse is the envelope for attachment list endpoints.
type listAttachmentsResponse struct {
	Attachments []*Attachment `json:"attachments"`
}

// ListMemoAttachments fetches the attachments belonging to one memo via
// GET /api/v1/memos/{uid}/attachments.
func (c *Client) ListMemoAttachments(ctx context.Context, memoUID string) ([]*Attachment, error) {
	var resp listAttachmentsResponse
	path := "/api/v1/memos/" + url.PathEscape(memoUID) + "/attachments"
	if err := c.do(ctx, methodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Attachments, nil
}

// DownloadFile streams an attachment blob to w. name is the resource name
// ("attachments/{id}"); filename is the original file name. It targets the file
// server route /file/{name}/{filename} with the bearer token attached, and
// returns the number of bytes written.
func (c *Client) DownloadFile(ctx context.Context, name, filename string, w io.Writer) (int64, error) {
	// Build /file/attachments/{id}/{filename}, path-escaping each segment but
	// keeping the slashes.
	segs := strings.Split(strings.Trim(name, "/"), "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	path := "/file/" + strings.Join(segs, "/") + "/" + url.PathEscape(filename)

	req := c.downloadHTTP.R().
		SetContext(ctx).
		DisableAutoReadResponse()
	if c.token != "" {
		req.SetBearerAuthToken(c.token)
	}
	resp, err := req.Send(methodGet, c.baseURL+path)
	if err != nil {
		return 0, fmt.Errorf("download %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return 0, fmt.Errorf("download %s -> %d: %s", path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return io.Copy(w, resp.Body)
}
