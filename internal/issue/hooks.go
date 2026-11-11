package issue

import (
	"context"
	"fmt"
)

// HookManager is the optional "manage the host's webhook registrations"
// capability. It is deliberately NOT part of the Provider interface: only
// GitHub supports it today, and forcing GitCode to grow no-op methods just to
// satisfy a shared interface is worse than one type assertion.
type HookManager interface {
	ListHooks(ctx context.Context, repo string) ([]Webhook, error)
	CreateHook(ctx context.Context, repo string, spec HookSpec) (Webhook, error)
	UpdateHook(ctx context.Context, repo string, id int64, spec HookSpec) (Webhook, error)
}

// Webhook is one registered webhook (host API shape, minimal).
type Webhook struct {
	ID     int64    `json:"id"`
	URL    string   `json:"url"`
	Active bool     `json:"active"`
	Events []string `json:"events"`
}

// HookSpec is the desired registration: URL + secret + subscribed events.
type HookSpec struct {
	URL    string
	Secret string
	Events []string
}

var _ HookManager = (*Client)(nil)

// githubHook is GitHub's hook shape for reads. config.url is where GitHub
// delivers; events is the subscription list.
type githubHook struct {
	ID     int64    `json:"id"`
	Active bool     `json:"active"`
	Events []string `json:"events"`
	Config struct {
		URL string `json:"url"`
	} `json:"config"`
}

// githubHookWrite is the create/update body (POST and PATCH share it). The
// secret is omitted when empty so an update never wipes an existing secret.
type githubHookWrite struct {
	Name   string   `json:"name"`
	Active bool     `json:"active"`
	Events []string `json:"events"`
	Config struct {
		URL         string `json:"url"`
		ContentType string `json:"content_type"`
		Secret      string `json:"secret,omitempty"`
		InsecureSSL string `json:"insecure_ssl"`
	} `json:"config"`
}

func (c *Client) ListHooks(ctx context.Context, repo string) ([]Webhook, error) {
	var hooks []githubHook
	if err := c.do(ctx, "GET", "/repos/"+repo+"/hooks", nil, &hooks); err != nil {
		return nil, err
	}
	out := make([]Webhook, 0, len(hooks))
	for _, h := range hooks {
		events := h.Events
		if events == nil {
			events = []string{}
		}
		out = append(out, Webhook{ID: h.ID, URL: h.Config.URL, Active: h.Active, Events: events})
	}
	return out, nil
}

func (c *Client) CreateHook(ctx context.Context, repo string, spec HookSpec) (Webhook, error) {
	var res githubHook
	if err := c.do(ctx, "POST", "/repos/"+repo+"/hooks", githubHookBody(spec), &res); err != nil {
		return Webhook{}, err
	}
	return hookFromGitHub(res), nil
}

func (c *Client) UpdateHook(ctx context.Context, repo string, id int64, spec HookSpec) (Webhook, error) {
	var res githubHook
	path := fmt.Sprintf("/repos/%s/hooks/%d", repo, id)
	if err := c.do(ctx, "PATCH", path, githubHookBody(spec), &res); err != nil {
		return Webhook{}, err
	}
	return hookFromGitHub(res), nil
}

func githubHookBody(spec HookSpec) githubHookWrite {
	body := githubHookWrite{Name: "web", Active: true, Events: spec.Events}
	body.Config.URL = spec.URL
	body.Config.ContentType = "json"
	body.Config.Secret = spec.Secret
	body.Config.InsecureSSL = "0"
	return body
}

func hookFromGitHub(h githubHook) Webhook {
	events := h.Events
	if events == nil {
		events = []string{}
	}
	return Webhook{ID: h.ID, URL: h.Config.URL, Active: h.Active, Events: events}
}
