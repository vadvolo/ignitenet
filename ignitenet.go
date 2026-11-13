package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Dialer opens a TCP connection to a device. Implemented by *Tunnel (via the
// SSH jump host) and by *net.Dialer (direct).
type Dialer interface {
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
}

// session holds the authenticated state for one device endpoint: the LuCI
// session token (stok, carried in the URL path) and the sysauth cookie value.
type session struct {
	client  *http.Client
	stok    string
	sysauth string
}

// Client talks to IgniteNet (LuCI/"acn") devices through a Dialer. It keeps one
// authenticated session per device endpoint ("ip:port") and transparently
// re-authenticates when the session expires (the device then serves the HTML
// login page instead of JSON).
type Client struct {
	cfg    Config
	dialer Dialer

	mu       sync.Mutex
	sessions map[string]*session // keyed by "ip:port"
	ports    map[string]string   // ip -> port where login last succeeded
}

func NewClient(cfg Config, dialer Dialer) *Client {
	return &Client{
		cfg:      cfg,
		dialer:   dialer,
		sessions: make(map[string]*session),
		ports:    make(map[string]string),
	}
}

func (c *Client) sessionFor(endpoint string) *session {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.sessions[endpoint]; ok {
		return s
	}
	s := &session{
		client: &http.Client{
			Timeout: 20 * time.Second,
			// Do not follow redirects: the login POST answers 302 and we need
			// to read its Location (for stok) and Set-Cookie (for sysauth).
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Transport: &http.Transport{
				DialContext:         c.dialer.DialContext,
				TLSHandshakeTimeout: 10 * time.Second,
			},
		},
	}
	c.sessions[endpoint] = s
	return s
}

func (c *Client) drop(endpoint string) {
	c.mu.Lock()
	delete(c.sessions, endpoint)
	c.mu.Unlock()
}

func (c *Client) endpoint(ip, port string) string {
	if port == "" {
		port = strconv.Itoa(c.cfg.DevicePort)
	}
	return net.JoinHostPort(ip, port)
}

// --- port cache -----------------------------------------------------------

func (c *Client) cachedPort(ip string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.ports[ip]
	return p, ok
}

func (c *Client) rememberPort(ip, port string) {
	c.mu.Lock()
	c.ports[ip] = port
	n := len(c.ports)
	c.mu.Unlock()
	portCacheSize.Set(float64(n))
}

func (c *Client) forgetPort(ip string) {
	c.mu.Lock()
	delete(c.ports, ip)
	n := len(c.ports)
	c.mu.Unlock()
	portCacheSize.Set(float64(n))
}

func (c *Client) PortCache() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]string, len(c.ports))
	for k, v := range c.ports {
		out[k] = v
	}
	return out
}

// --- auth + fetch ----------------------------------------------------------

// login authenticates against the device and stores the stok + sysauth on the
// session. reachable reports whether the device answered at all.
func (c *Client) login(s *session, endpoint string) (reachable bool, err error) {
	form := url.Values{
		"username":  {c.cfg.Username},
		"password":  {c.cfg.Password},
		"login_msg": {""},
	}
	resp, err := s.client.PostForm("http://"+endpoint+"/cgi-bin/acn/", form)
	if err != nil {
		return false, fmt.Errorf("login request: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))

	// Success is a 302 whose Location carries ;stok=<token>; a failed login
	// just re-serves the login page with HTTP 200.
	loc := resp.Header.Get("Location")
	stok := stokFromLocation(loc)
	if resp.StatusCode != http.StatusFound || stok == "" {
		return true, fmt.Errorf("login failed (HTTP %d) — check USERNAME/PASSWORD", resp.StatusCode)
	}
	var sysauth string
	for _, ck := range resp.Cookies() {
		if ck.Name == "sysauth" {
			sysauth = ck.Value
		}
	}
	if sysauth == "" {
		return true, fmt.Errorf("login succeeded but no sysauth cookie returned")
	}
	s.stok = stok
	s.sysauth = sysauth
	return true, nil
}

// stokFromLocation extracts the token from a Location like
// "/cgi-bin/acn/;stok=364dbd...".
func stokFromLocation(loc string) string {
	const marker = "stok="
	i := strings.Index(loc, marker)
	if i < 0 {
		return ""
	}
	tok := loc[i+len(marker):]
	// token runs until a path separator or end
	if j := strings.IndexAny(tok, "/;?&"); j >= 0 {
		tok = tok[:j]
	}
	return strings.TrimSpace(tok)
}

// Fetch retrieves an "acn" resource from a device, consulting and maintaining
// the per-IP port cache (see the mimosa-proxy analogue). path is everything
// after ";stok=<token>/", e.g. "admin/status/connected_clients?ifname=ath1".
func (c *Client) Fetch(ip, port, path string) ([]byte, string, error) {
	port = strings.TrimSpace(port)
	forced := false
	if cp, ok := c.cachedPort(ip); ok {
		port = cp
		forced = true
	}
	endpoint := c.endpoint(ip, port)
	_, usedPort, _ := net.SplitHostPort(endpoint)

	start := time.Now()
	body, ct, reachable, err := c.do(endpoint, path)
	deviceFetchDuration.Observe(time.Since(start).Seconds())
	switch {
	case err == nil:
		deviceFetchTotal.WithLabelValues("success").Inc()
		c.rememberPort(ip, usedPort)
		return body, ct, nil
	case reachable:
		deviceFetchTotal.WithLabelValues("http_error").Inc()
		c.rememberPort(ip, usedPort)
		return body, ct, err
	default:
		deviceFetchTotal.WithLabelValues("unreachable").Inc()
		c.forgetPort(ip)
		c.drop(endpoint)
		if forced {
			return nil, "", fmt.Errorf("cached port %s no longer works for %s (cache cleared, retry with another port): %w", usedPort, ip, err)
		}
		return nil, "", fmt.Errorf("device %s unreachable (try another port): %w", endpoint, err)
	}
}

func (c *Client) do(endpoint, path string) (body []byte, contentType string, reachable bool, err error) {
	path = strings.TrimPrefix(path, "/")
	s := c.sessionFor(endpoint)

	for attempt := 0; attempt < 2; attempt++ {
		if s.stok == "" || s.sysauth == "" {
			okReach, lErr := c.login(s, endpoint)
			if lErr != nil {
				if !okReach {
					c.drop(endpoint)
				}
				return nil, "", okReach, lErr
			}
		}

		reqURL := "http://" + endpoint + "/cgi-bin/acn/;stok=" + s.stok + "/" + path
		body, ct, status, gErr := c.get(s, reqURL)
		if gErr != nil {
			c.drop(endpoint)
			return nil, "", false, gErr // transport failure -> not reachable
		}

		// An expired session is served the HTML login page (HTTP 200). Detect
		// non-JSON and re-authenticate once.
		if looksLikeLogin(body, ct) {
			s.stok, s.sysauth = "", ""
			continue
		}
		if status < 200 || status >= 300 {
			return body, ct, true, fmt.Errorf("device returned HTTP %d", status)
		}
		return body, ct, true, nil
	}
	return nil, "", true, fmt.Errorf("authentication to %s succeeded but %s still returned the login page", endpoint, path)
}

func (c *Client) get(s *session, reqURL string) (body []byte, contentType string, status int, err error) {
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, "", 0, err
	}
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	req.Header.Set("Cookie", "sysauth="+s.sysauth)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, "", 0, err
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", resp.StatusCode, err
	}
	return body, resp.Header.Get("Content-Type"), resp.StatusCode, nil
}

// looksLikeLogin reports whether a response is the HTML login page rather than
// a JSON API reply (the device's way of signalling an expired session).
func looksLikeLogin(body []byte, contentType string) bool {
	if strings.Contains(contentType, "text/html") {
		return true
	}
	trimmed := strings.TrimSpace(string(body))
	return strings.HasPrefix(trimmed, "<")
}
