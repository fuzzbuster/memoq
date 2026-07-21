package memos

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAddMemoReference_PreservesExistingRelations(t *testing.T) {
	var patchBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/memos/source/relations"):
			_, _ = w.Write([]byte(`{"relations":[{"memo":{"name":"memos/source"},"relatedMemo":{"name":"memos/existing"},"type":"REFERENCE"}]}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/memos/source/relations":
			patchBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "t")
	if _, err := c.AddMemoReference(context.Background(), "source", "target"); err != nil {
		t.Fatalf("AddMemoReference: %v", err)
	}
	var request struct {
		Name      string `json:"name"`
		Relations []struct {
			RelatedMemo struct {
				Name string `json:"name"`
			} `json:"relatedMemo"`
			Type string `json:"type"`
		} `json:"relations"`
	}
	if err := json.Unmarshal(patchBody, &request); err != nil {
		t.Fatalf("decode PATCH body: %v", err)
	}
	if request.Name != "memos/source" || len(request.Relations) != 2 {
		t.Fatalf("PATCH body = %s", patchBody)
	}
	if request.Relations[0].RelatedMemo.Name != "memos/existing" ||
		request.Relations[1].RelatedMemo.Name != "memos/target" ||
		request.Relations[1].Type != "REFERENCE" {
		t.Errorf("relations were not preserved and appended: %s", patchBody)
	}
}

func TestAddMemoReference_UsesObjectFormatAndAcceptsEmptySuccess(t *testing.T) {
	var patchBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/memos/source/relations"):
			_, _ = w.Write([]byte(`{"relations":[]}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/memos/source/relations":
			patchBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "t")
	raw, err := c.AddMemoReference(context.Background(), "memos/source", "memos/target")
	if err != nil {
		t.Fatalf("AddMemoReference: %v", err)
	}
	if string(raw) != `{}` {
		t.Fatalf("empty success response = %s, want {}", raw)
	}
	var request struct {
		Relations []struct {
			Memo struct {
				Name string `json:"name"`
			} `json:"memo"`
			RelatedMemo struct {
				Name string `json:"name"`
			} `json:"relatedMemo"`
			Type string `json:"type"`
		} `json:"relations"`
	}
	if err := json.Unmarshal(patchBody, &request); err != nil {
		t.Fatalf("decode PATCH body: %v", err)
	}
	if len(request.Relations) != 1 ||
		request.Relations[0].Memo.Name != "memos/source" ||
		request.Relations[0].RelatedMemo.Name != "memos/target" ||
		request.Relations[0].Type != "REFERENCE" {
		t.Errorf("unexpected object relation: %s", patchBody)
	}
}

func TestAddMemoReference_UsesStableV029ObjectFormat(t *testing.T) {
	var patchBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/memos/source/relations":
			_, _ = w.Write([]byte(`{"relations":[]}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/memos/source/relations":
			patchBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "t")
	if _, err := c.AddMemoReference(context.Background(), "source", "target"); err != nil {
		t.Fatalf("AddMemoReference: %v", err)
	}
	var request struct {
		Relations []struct {
			Memo struct {
				Name string `json:"name"`
			} `json:"memo"`
			RelatedMemo struct {
				Name string `json:"name"`
			} `json:"relatedMemo"`
			Type string `json:"type"`
		} `json:"relations"`
	}
	if err := json.Unmarshal(patchBody, &request); err != nil {
		t.Fatalf("decode PATCH body: %v", err)
	}
	if len(request.Relations) != 1 ||
		request.Relations[0].Memo.Name != "memos/source" ||
		request.Relations[0].RelatedMemo.Name != "memos/target" ||
		request.Relations[0].Type != "REFERENCE" {
		t.Errorf("unexpected v0.29 relation: %s", patchBody)
	}
}

func TestAddMemoReference_PreservesPaginatedObjectRelations(t *testing.T) {
	var patchBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/memos/source/relations" && r.URL.Query().Get("pageToken") == "":
			_, _ = w.Write([]byte(`{"relations":[{"memo":{"name":"memos/source"},"relatedMemo":{"name":"memos/first"},"type":"REFERENCE"}],"nextPageToken":"next"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/memos/source/relations" && r.URL.Query().Get("pageToken") == "next":
			_, _ = w.Write([]byte(`{"relations":[{"memo":{"name":"memos/source"},"relatedMemo":{"name":"memos/second"},"type":"REFERENCE"}]}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/memos/source/relations":
			patchBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "t")
	if _, err := c.AddMemoReference(context.Background(), "source", "target"); err != nil {
		t.Fatalf("AddMemoReference: %v", err)
	}
	var request struct {
		Relations []struct {
			RelatedMemo struct {
				Name string `json:"name"`
			} `json:"relatedMemo"`
		} `json:"relations"`
	}
	if err := json.Unmarshal(patchBody, &request); err != nil {
		t.Fatalf("decode PATCH body: %v", err)
	}
	if len(request.Relations) != 3 {
		t.Fatalf("PATCH body = %s", patchBody)
	}
	for i, want := range []string{"memos/first", "memos/second", "memos/target"} {
		if request.Relations[i].RelatedMemo.Name != want {
			t.Errorf("relation %d = %q, want %q", i, request.Relations[i].RelatedMemo.Name, want)
		}
	}
}

func TestAddMemoReference_DoesNotDuplicateExistingReference(t *testing.T) {
	patchCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"relations":[{"memo":{"name":"memos/source"},"relatedMemo":{"name":"memos/target"},"type":"REFERENCE"}]}`))
		case http.MethodPatch:
			patchCalls++
			t.Error("existing reference should not trigger PATCH")
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "t")
	raw, err := c.AddMemoReference(context.Background(), "source", "target")
	if err != nil {
		t.Fatalf("AddMemoReference: %v", err)
	}
	if patchCalls != 0 || string(raw) != `{}` {
		t.Errorf("patch calls = %d, response = %s", patchCalls, raw)
	}
}
