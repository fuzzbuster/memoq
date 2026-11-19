package memos

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionSignInRefreshAndSignOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/signin":
			w.Header().Add("Grpc-Metadata-Set-Cookie", (&http.Cookie{Name: "memos_refresh", Value: "first", Path: "/api/auth", HttpOnly: true}).String())
			fmt.Fprint(w, `{"accessToken":"access1"}`)
		case "/api/auth/refresh":
			cookie, err := r.Cookie("memos_refresh")
			if err != nil || cookie.Value != "first" {
				t.Error("refresh cookie did not persist across clients")
			}
			if r.Header.Get("Grpc-Metadata-Cookie") != "memos_refresh=first" {
				t.Error("REST gateway refresh-cookie metadata missing")
			}
			http.SetCookie(w, &http.Cookie{Name: "memos_refresh", Value: "second", Path: "/api/auth", HttpOnly: true})
			fmt.Fprint(w, `{"accessToken":"access2"}`)
		case "/api/auth/me":
			if r.Header.Get("Authorization") != "Bearer access2" {
				t.Error("refreshed access token not used")
			}
			fmt.Fprint(w, `{}`)
		case "/api/auth/signout":
			cookie, err := r.Cookie("memos_refresh")
			if err != nil || cookie.Value != "second" {
				t.Error("rotated cookie not used for signout")
			}
			http.SetCookie(w, &http.Cookie{Name: "memos_refresh", Value: "", Path: "/api/auth", MaxAge: -1})
			fmt.Fprint(w, `{}`)
		}
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "session.json")
	client := New(srv.URL, "")
	_ = client.UseSessionFile(path)
	ctx := context.Background()
	if _, err := client.Do(ctx, "POST", "/api/auth/signin", nil); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("session file permissions: %v, %v", fi, err)
	}
	client = New(srv.URL, "")
	if err := client.UseSessionFile(path); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(ctx, "POST", "/api/auth/refresh", nil); err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	pat := New(srv.URL, "pat")
	_ = pat.UseSessionFile(path)
	if pat.token != "pat" {
		t.Fatal("session overrides explicit PAT")
	}
	other := New("https://other.example", "")
	_ = other.UseSessionFile(path)
	if other.token != "" {
		t.Fatal("session leaked to a different server")
	}
	client = New(srv.URL, "")
	_ = client.UseSessionFile(path)
	if _, err := client.Do(ctx, "POST", "/api/auth/signout", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) || client.token != "" {
		t.Fatal("signout did not clear session")
	}
}
