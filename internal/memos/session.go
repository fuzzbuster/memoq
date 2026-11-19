package memos

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type session struct {
	ServerURL   string       `json:"server_url"`
	AccessToken string       `json:"access_token"`
	Refresh     *http.Cookie `json:"refresh_cookie,omitempty"`
}

// UseSessionFile loads the sign-in session for this server. An explicit PAT
// takes precedence over its access token.
func (c *Client) UseSessionFile(path string) error {
	c.sessionFile = path
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var saved session
	if err := json.Unmarshal(data, &saved); err != nil {
		return fmt.Errorf("read session: %w", err)
	}
	if saved.ServerURL != c.baseURL {
		return nil
	}
	c.session = saved
	if c.token == "" {
		c.token = saved.AccessToken
	}
	if saved.Refresh != nil {
		u, err := url.Parse(c.baseURL)
		if err != nil {
			return err
		}
		jar, _ := cookiejar.New(nil)
		jar.SetCookies(u, []*http.Cookie{saved.Refresh})
		c.http.SetCookieJar(jar)
		c.downloadHTTP.SetCookieJar(jar)
	}
	return nil
}

func (c *Client) updateSession(method, path string, data []byte, cookies []*http.Cookie) {
	path, _, _ = strings.Cut(path, "?")
	if c.sessionFile == "" || method != methodPost {
		return
	}
	switch path {
	case "/api/auth/signin", "/api/auth/refresh":
		var response struct {
			AccessToken string `json:"accessToken"`
		}
		if json.Unmarshal(data, &response) != nil || response.AccessToken == "" {
			return
		}
		c.session.ServerURL = c.baseURL
		c.session.AccessToken = response.AccessToken
		if !c.explicitToken {
			c.token = response.AccessToken
		}
		for _, cookie := range cookies {
			if cookie.Name == "memos_refresh" {
				c.session.Refresh = cookie
				if u, err := url.Parse(c.baseURL); err == nil {
					c.http.GetClient().Jar.SetCookies(u, []*http.Cookie{cookie})
				}
			}
		}
		if err := c.saveSession(); err != nil {
			fmt.Fprintln(os.Stderr, "memoq: authenticated, but cannot save session:", err)
		}
	case "/api/auth/signout":
		c.session = session{}
		if !c.explicitToken {
			c.token = ""
		}
		if err := os.Remove(c.sessionFile); err != nil && !os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "memoq: signed out, but cannot remove session:", err)
		}
	}
}

func (c *Client) saveSession() error {
	data, err := json.Marshal(c.session)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(c.sessionFile), ".session-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), c.sessionFile)
}
