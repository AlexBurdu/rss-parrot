package shared

import (
	"fmt"
	"net/http"
)

//go:generate mockgen --build_flags=--mod=mod -destination ../test/mocks/mock_user_agent.go -package mocks rss_parrot/shared IUserAgent

// Use a browser-like User-Agent for feed fetching.
// Many sites (Medium, Cloudflare-protected) return 403
// to bot User-Agents.
const feedUserAgent = "Mozilla/5.0 (compatible; RSSParrot; +https://%s)"

type IUserAgent interface {
	AddUserAgent(req *http.Request)
}

type userAgent struct {
	userAgentValue string
}

func NewUserAgent(cfg *Config) IUserAgent {
	return &userAgent{
		userAgentValue: fmt.Sprintf(feedUserAgent, cfg.Host),
	}
}

func (ua *userAgent) AddUserAgent(req *http.Request) {
	req.Header.Add("User-Agent", ua.userAgentValue)
}
