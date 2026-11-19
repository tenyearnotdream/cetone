package ncm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Endpoints. The web API lives on music.163.com; eapi calls go to
// interface.music.163.com with the same path, so one constant names the path
// the signature covers and the other the host it is sent to.
const (
	webHost       = "https://music.163.com"
	interfaceHost = "https://interface.music.163.com"

	// userAgent is what the desktop client presents. The API refuses some
	// endpoints to a bare Go user agent.
	userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

	// requestTimeout bounds one call. A search or a playlist of ten thousand
	// songs is the slow case; a hung socket is not worth waiting through.
	requestTimeout = 30 * time.Second
)

// ErrUnsupported reports a request this client cannot make: an empty id, a
// search type it does not know.
var ErrUnsupported = fmt.Errorf("ncm: unsupported request")

// APIError is a response with a non-200 code, or a malformed envelope.
type APIError struct {
	Path string
	Code int
	Msg  string
}

func (e *APIError) Error() string {
	if e.Msg != "" {
		return fmt.Sprintf("ncm: %s: code %d: %s", e.Path, e.Code, e.Msg)
	}
	return fmt.Sprintf("ncm: %s: code %d", e.Path, e.Code)
}

// cookieStore keeps the session cookies in the order they were set, so a
// saved session is a plain map and the header is stable between runs.
type cookieStore struct {
	mu      sync.RWMutex
	values  map[string]string
	base    string
	baseSet bool
}

func newCookieStore() *cookieStore {
	return &cookieStore{values: make(map[string]string)}
}

// deviceCookies are the fields that make this client one device to the API. They
// are created once, on the first launch that has no saved session, and then kept
// for the life of the installation: a client whose device id changes on every
// start looks like a new device each time, which is what a login is remembered
// against.
var deviceCookies = []string{"_ntes_nuid", "_ntes_nnid", "NMTID", "WNMCID", "WEVNSM", "__remember_me"}

// requestCookies belong to one request's headers, not to the session. They are
// never stored, so they are not written to the session file either.
var requestCookies = []string{"os", "appver", "osver", "deviceId"}

// load restores cookies saved by Cookies. A saved session already names a
// device, so the device fields are kept as they are instead of being generated
// again on the next request.
func (s *cookieStore) load(values map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range values {
		if k == "" || v == "" {
			continue
		}
		s.values[k] = v
	}
	for _, name := range deviceCookies {
		if s.values[name] != "" {
			s.baseSet = true
			break
		}
	}
}

// snapshot returns the cookies to save to disk: the device fields and the
// session's own, without the per-request ones.
func (s *cookieStore) snapshot() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.values))
	for k, v := range s.values {
		if k == "" || v == "" {
			continue
		}
		out[k] = v
	}
	for _, name := range requestCookies {
		delete(out, name)
	}
	return out
}

func (s *cookieStore) get(name string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.values[name]
}

func (s *cookieStore) set(name, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value == "" {
		delete(s.values, name)
		return
	}
	s.values[name] = value
}

func (s *cookieStore) clearSession() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, name := range []string{"MUSIC_U", "MUSIC_A", "__csrf", "NMTID"} {
		delete(s.values, name)
	}
	s.baseSet = false
}

// header builds the Cookie header for one request: the device fields that
// identify this installation, the login token, then whatever the server set
// while the session ran.
//
// It is a pure read of the stored cookies. The fields that differ between the
// weapi and eapi transports are added to the returned string only, never to the
// store: they belong to the request, and writing them into the session would
// both outlive their meaning and race with the requests running beside this one.
func (s *cookieStore) header(mode encryptMode) string {
	fields := s.fields(mode)
	parts := make([]string, 0, len(fields)+1)
	for _, field := range fields {
		parts = append(parts, field.name+"="+field.value)
	}
	parts = append(parts, "channel=")
	return strings.Join(parts, "; ")
}

// nameValue is one cookie field, ready to be written into a header.
type nameValue struct{ name, value string }

// fields is the ordered content of the Cookie header: the device and session
// cookies, and the transport's own fields in place of the defaults.
func (s *cookieStore) fields(mode encryptMode) []nameValue {
	s.mu.Lock()
	if !s.baseSet {
		s.initDeviceLocked()
	}
	s.mu.Unlock()

	osName, appver, osver := "pc", "2.7.1.198277", "10"
	if mode == modeEapi {
		osName, appver, osver = "iphone", "9.0.90", "16.2"
	}

	s.mu.RLock()
	stored := make(map[string]string, len(s.values))
	for name, value := range s.values {
		stored[name] = value
	}
	s.mu.RUnlock()

	// An anonymous visitor is identified by the device id.
	if stored["MUSIC_U"] == "" {
		stored["deviceId"] = stored["_ntes_nuid"]
	}
	transport := map[string]string{"os": osName, "appver": appver, "osver": osver}
	for name, value := range transport {
		stored[name] = value
	}
	for _, name := range requestCookies {
		if name == "deviceId" {
			continue
		}
		stored[name] = transport[name]
	}

	names := make([]string, 0, len(stored))
	for name := range stored {
		if name == "" || stored[name] == "" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]nameValue, 0, len(names))
	for _, name := range names {
		out = append(out, nameValue{name: name, value: stored[name]})
	}
	return out
}

// initDeviceLocked gives a client that has never run a device identity. A loaded
// session keeps the one it was saved with.
func (s *cookieStore) initDeviceLocked() {
	now := strconv.FormatInt(time.Now().UnixMilli(), 10)
	nuid := randomHex(16)
	s.values["_ntes_nuid"] = nuid
	s.values["_ntes_nnid"] = nuid + "," + now
	s.values["NMTID"] = randomHex(8)
	s.values["WNMCID"] = fmt.Sprintf("%02x%02x%02x.%s", randomByte(), randomByte(), randomByte(), now)
	s.values["WEVNSM"] = "1.0.0"
	s.values["__remember_me"] = "true"
	s.baseSet = true
}

// Client is one NetEase Cloud Music session. It is safe for concurrent use:
// the UI runs several requests at once and they share its cookies.
type Client struct {
	http    *http.Client
	cookies *cookieStore

	mu   sync.RWMutex
	csrf string

	// LoggedIn reports whether a login token is present. It is a hint for the
	// UI; the server still decides, and any request may answer code 301.
	muLogin sync.RWMutex
	login   bool
}

// NewClient builds a client with no session. Cookies can be loaded before the
// first request with LoadCookies.
func NewClient() *Client {
	transport := &http.Transport{
		Proxy:       http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		// The API is HTTP/1.1 over TLS; a short idle window keeps a closed
		// connection from being reused.
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &Client{
		http: &http.Client{
			Transport: transport,
			Timeout:   requestTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		cookies: newCookieStore(),
	}
}

// SetProxy points every request at a proxy URL (http, https, socks5). An empty
// string goes back to the environment's proxy settings.
func (c *Client) SetProxy(proxyURL string) error {
	transport, ok := c.http.Transport.(*http.Transport)
	if !ok {
		return fmt.Errorf("ncm: transport is not configurable")
	}
	if proxyURL == "" {
		transport.Proxy = http.ProxyFromEnvironment
		return nil
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return fmt.Errorf("ncm: parse proxy: %w", err)
	}
	if parsed.Scheme == "" {
		parsed, err = url.Parse("http://" + proxyURL)
		if err != nil {
			return fmt.Errorf("ncm: parse proxy: %w", err)
		}
	}
	transport.Proxy = http.ProxyURL(parsed)
	return nil
}

// LoadCookies restores a saved session.
func (c *Client) LoadCookies(values map[string]string) {
	c.cookies.load(values)
	if csrf := c.cookies.get("__csrf"); csrf != "" {
		c.mu.Lock()
		c.csrf = csrf
		c.mu.Unlock()
	}
	c.muLogin.Lock()
	c.login = c.cookies.get("MUSIC_U") != ""
	c.muLogin.Unlock()
}

// Cookies returns the session cookies to persist.
func (c *Client) Cookies() map[string]string { return c.cookies.snapshot() }

// LoggedIn reports whether the session holds a login token.
func (c *Client) LoggedIn() bool {
	c.muLogin.RLock()
	defer c.muLogin.RUnlock()
	return c.login
}

// csrfToken is the token the server hands out with the first response and the
// weapi body must echo.
func (c *Client) csrfToken() string {
	c.mu.RLock()
	csrf := c.csrf
	c.mu.RUnlock()
	if csrf != "" {
		return csrf
	}
	if csrf = c.cookies.get("__csrf"); csrf != "" {
		c.mu.Lock()
		c.csrf = csrf
		c.mu.Unlock()
	}
	return csrf
}

// post sends an encrypted POST and returns the JSON body. params are strings
// because that is what every endpoint takes; JSON-typed params are marshalled
// with jsonValue.
func (c *Client) post(ctx context.Context, mode encryptMode, path string, params map[string]string) ([]byte, error) {
	params = cloneParams(params)
	csrf := c.csrfToken()

	// Every request carries an empty csrf_token unless the path is the ones
	// that must not have it.
	query := ""
	body := make([]byte, 0, 256)
	vals := url.Values(nil)
	switch mode {
	case modeWeapi:
		params["csrf_token"] = csrf
		query = "?csrf_token=" + url.QueryEscape(csrf)
		payload, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		vals, err = weapiPayload(payload)
		if err != nil {
			return nil, err
		}
	case modeEapi:
		params["csrf_token"] = csrf
		object := make(map[string]any, len(params)+1)
		for k, v := range params {
			object[k] = v
		}
		header := map[string]any{
			"osver":       "16.2",
			"deviceId":    c.cookies.get("_ntes_nuid"),
			"os":          "iPhone OS",
			"appver":      "9.0.90",
			"versioncode": "140",
			"mobilename":  "",
			"buildver":    strconv.FormatInt(time.Now().Unix(), 10),
			"resolution":  "1920x1080",
			"__csrf":      csrf,
			"requestId":   fmt.Sprintf("%d_%04d", time.Now().UnixMilli(), int(randomByte())%1000),
			"channel":     "",
		}
		if v := c.cookies.get("MUSIC_U"); v != "" {
			header["MUSIC_U"] = v
		}
		if v := c.cookies.get("MUSIC_A"); v != "" {
			header["MUSIC_A"] = v
		}
		object["header"] = header
		payload, err := json.Marshal(object)
		if err != nil {
			return nil, err
		}
		vals = eapiPayload(path, payload)
	}
	body = append(body, vals.Encode()...)

	host := webHost + path + query
	if mode == modeEapi {
		host = interfaceHost + strings.Replace(path, "/api", "/eapi", 1)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, host, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Host", req.URL.Host)
	req.Header.Set("Origin", webHost)
	req.Header.Set("Referer", webHost)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Cookie", c.cookies.header(mode))

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	for _, cookie := range resp.Cookies() {
		c.cookies.set(cookie.Name, cookie.Value)
		if cookie.Name == "__csrf" && cookie.Value != "" {
			c.mu.Lock()
			c.csrf = cookie.Value
			c.mu.Unlock()
		}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ncm: %s: HTTP %d", path, resp.StatusCode)
	}
	if looksEncrypted(data) {
		return eapiDecryptResponse(data)
	}
	return data, nil
}

// call sends a request and unmarshals the answer into out, turning the API's
// own error codes into an *APIError.
func (c *Client) call(ctx context.Context, mode encryptMode, path string, params map[string]string, out any) error {
	data, err := c.post(ctx, mode, path, params)
	if err != nil {
		return err
	}
	var envelope struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("ncm: %s: decode: %w", path, err)
	}
	if envelope.Code != 200 {
		return &APIError{Path: path, Code: envelope.Code, Msg: envelope.Msg}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("ncm: %s: decode: %w", path, err)
	}
	return nil
}

// getJSON reads a plain GET (cover images, song files, lyric text).
func (c *Client) getJSON(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", webHost)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ncm: GET %s: HTTP %d", rawURL, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

func cloneParams(params map[string]string) map[string]string {
	out := make(map[string]string, len(params)+1)
	for k, v := range params {
		out[k] = v
	}
	return out
}

// jsonValue marshals a struct or slice into the JSON text an endpoint expects
// in one string parameter (the ids of a song detail call, for example).
func jsonValue(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func idString(id int64) string { return strconv.FormatInt(id, 10) }

func uintString(n int) string { return strconv.Itoa(n) }

// SetTransport replaces the HTTP transport, for a test that must not reach the
// network and for an application that wants its own connection pool.
func (c *Client) SetTransport(transport http.RoundTripper) {
	c.http.Transport = transport
}
