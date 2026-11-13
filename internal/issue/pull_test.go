package issue

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGitHubCreatePull: POST /repos/{repo}/pulls with the JSON payload, then
// return html_url + number.
func TestGitHubCreatePull(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"html_url":"https://github.com/o/r/pull/12","number":12}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := &Client{token: "tok", http: srv.Client(), baseURL: srv.URL}
	url, number, err := c.CreatePull(context.Background(), "o/r", "Update skill x", "agentwork/skill-abc", "main", "body text")
	if err != nil {
		t.Fatalf("CreatePull: %v", err)
	}
	if gotPath != "/repos/o/r/pulls" || gotAuth != "Bearer tok" {
		t.Fatalf("request = %s auth=%q", gotPath, gotAuth)
	}
	if gotBody["title"] != "Update skill x" || gotBody["head"] != "agentwork/skill-abc" ||
		gotBody["base"] != "main" || gotBody["body"] != "body text" {
		t.Fatalf("payload = %+v", gotBody)
	}
	if url != "https://github.com/o/r/pull/12" || number != 12 {
		t.Fatalf("CreatePull = %q #%d", url, number)
	}
}

// TestGitHubCheckPushPermission: identity from /user, permission from
// /repos/{repo} permissions.push (or admin).
func TestGitHubCheckPushPermission(t *testing.T) {
	cases := []struct {
		name     string
		repoJSON string
		want     bool
	}{
		{"push", `{"permissions":{"push":true,"pull":true}}`, true},
		{"admin", `{"permissions":{"admin":true,"push":false}}`, true},
		{"read only", `{"permissions":{"pull":true,"push":false,"admin":false}}`, false},
		{"no permissions object", `{}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/user":
					_, _ = w.Write([]byte(`{"login":"octocat"}`))
				case "/repos/o/r":
					_, _ = w.Write([]byte(c.repoJSON))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()
			cl := &Client{token: "tok", http: srv.Client(), baseURL: srv.URL}
			identity, canPush, err := cl.CheckPushPermission(context.Background(), "o/r")
			if err != nil {
				t.Fatalf("CheckPushPermission: %v", err)
			}
			if identity != "octocat" || canPush != c.want {
				t.Fatalf("CheckPushPermission = %q %v, want octocat %v", identity, canPush, c.want)
			}
		})
	}
}

// TestGitCodeCreatePull: POST form with title/head/base/body; response URL
// may be html_url or web_url and the number may be number or iid.
func TestGitCodeCreatePull(t *testing.T) {
	var gotPath, gotForm, gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotToken = r.URL.Query().Get("access_token")
		_ = r.ParseForm()
		gotForm = r.PostForm.Encode()
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"web_url":"https://gitcode.com/o/r/pulls/7","iid":"7"}`))
	}))
	defer srv.Close()

	c := &GitCodeClient{token: "tok", http: srv.Client(), baseURL: srv.URL}
	url, number, err := c.CreatePull(context.Background(), "o/r", "Update", "agentwork/skill-x", "main", "why")
	if err != nil {
		t.Fatalf("CreatePull: %v", err)
	}
	if gotPath != "/repos/o/r/pulls" || gotToken != "tok" {
		t.Fatalf("request path=%s token=%q", gotPath, gotToken)
	}
	for _, want := range []string{"title=Update", "head=agentwork%2Fskill-x", "base=main", "body=why"} {
		if !strings.Contains(gotForm, want) {
			t.Fatalf("form %q missing %q", gotForm, want)
		}
	}
	if url != "https://gitcode.com/o/r/pulls/7" || number != 7 {
		t.Fatalf("CreatePull = %q #%d", url, number)
	}
}

// TestGitCodeCheckPushPermission: identity from /user and either permission
// shape (permission / permissions) accepted; push=false is reported.
func TestGitCodeCheckPushPermission(t *testing.T) {
	cases := []struct {
		name     string
		repoJSON string
		want     bool
	}{
		{"permission push", `{"permission":{"push":true}}`, true},
		{"permissions admin", `{"permissions":{"admin":true}}`, true},
		{"read only", `{"permission":{"pull":true,"push":false},"permissions":{"push":false,"admin":false}}`, false},
		{"no permission object", `{"name":"o/r"}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/user":
					_, _ = w.Write([]byte(`{"login":"gitcoder"}`))
				case "/repos/o/r":
					_, _ = w.Write([]byte(c.repoJSON))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()
			cl := &GitCodeClient{token: "tok", http: srv.Client(), baseURL: srv.URL}
			identity, canPush, err := cl.CheckPushPermission(context.Background(), "o/r")
			if err != nil {
				t.Fatalf("CheckPushPermission: %v", err)
			}
			if identity != "gitcoder" || canPush != c.want {
				t.Fatalf("CheckPushPermission = %q %v, want gitcoder %v", identity, canPush, c.want)
			}
		})
	}
}
