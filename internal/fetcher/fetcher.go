package fetcher

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type Client struct {
	HTTP       *http.Client
	UserAgent  string
	MinDelay   time.Duration
	mu         sync.Mutex
	lastByHost map[string]time.Time
}

type Metadata struct {
	ContentType   string
	ContentLength int64
}

func New(userAgent string, timeout, minDelay time.Duration) *Client {
	if userAgent == "" {
		userAgent = "bc-permit-scraper/0.1 (+public permit status research; contact: set USER_AGENT)"
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if minDelay <= 0 {
		minDelay = 1500 * time.Millisecond
	}
	return &Client{
		HTTP:       &http.Client{Timeout: timeout},
		UserAgent:  userAgent,
		MinDelay:   minDelay,
		lastByHost: map[string]time.Time{},
	}
}

func (c *Client) Get(ctx context.Context, rawURL string, headers map[string]string) ([]byte, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", err
	}
	c.waitHost(u.Host)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json,text/html,text/plain;q=0.9,*/*;q=0.7")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, resp.Header.Get("Content-Type"), fmt.Errorf("GET %s returned %s: %s", rawURL, resp.Status, string(b))
	}
	b, err := io.ReadAll(resp.Body)
	return b, resp.Header.Get("Content-Type"), err
}

func (c *Client) Head(ctx context.Context, rawURL string, headers map[string]string) (Metadata, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return Metadata{}, err
	}
	c.waitHost(u.Host)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return Metadata{}, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json,text/html,text/plain;q=0.9,*/*;q=0.7")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Metadata{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Metadata{ContentType: resp.Header.Get("Content-Type"), ContentLength: resp.ContentLength}, fmt.Errorf("HEAD %s returned %s", rawURL, resp.Status)
	}
	return Metadata{ContentType: resp.Header.Get("Content-Type"), ContentLength: resp.ContentLength}, nil
}

func (c *Client) waitHost(host string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	last := c.lastByHost[host]
	wait := c.MinDelay - time.Since(last)
	if wait > 0 {
		time.Sleep(wait)
	}
	c.lastByHost[host] = time.Now()
}
