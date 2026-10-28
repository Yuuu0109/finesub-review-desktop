// Package client is the native desktop's transport for the review service.
// It owns authentication, CSRF, uploads and downloads; no browser is involved.
package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

const DefaultServer = "https://49.234.185.63"

// Secrets is implemented by the app's OS credential store. A missing item is
// os.ErrNotExist or an empty value; other errors must not be treated as a logout.
type Secrets interface {
	Get(string) ([]byte, error)
	Set(string, []byte) error
	Delete(string) error
}

type sessionCookie struct {
	Name    string    `json:"name"`
	Value   string    `json:"value"`
	Expires time.Time `json:"expires"`
	Secure  bool      `json:"secure"`
}

var cookieNames = []string{"review_access", "review_session"}

type Client struct {
	server  string
	origin  *url.URL
	http    *http.Client
	secrets Secrets
	mu      sync.Mutex
	cookies map[string]sessionCookie
	csrf    string
}

// APIError preserves the HTTP status without retaining request credentials or
// bodies. A mutation that fails is never retried automatically.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return e.Message }

// HTTPError is an alias for consumers that distinguish authorization errors.
type HTTPError = APIError

func StatusCode(err error) int {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status
	}
	return 0
}

// NormalizeServer accepts a service origin, not a path, URL with credentials,
// or an insecure public endpoint. HTTP is available only for local development.
func NormalizeServer(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.Opaque != "" {
		return "", errors.New("请输入完整服务器地址，例如 https://review.example.com")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	loopback := host == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", errors.New("服务器必须使用 HTTPS；本机调试可使用 HTTP 回环地址")
	}
	if host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(u.Host, "\\%") {
		return "", errors.New("只填写服务器域名和端口，不要附带账号、路径或参数")
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", errors.New("服务器端口无效")
		}
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" && !((u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80")) {
		host += ":" + port
	}
	return u.Scheme + "://" + host, nil
}

func New(server string, secrets Secrets) (*Client, error) {
	origin, err := NormalizeServer(server)
	if err != nil {
		return nil, err
	}
	if secrets == nil {
		return nil, errors.New("缺少系统凭据存储，无法保存审核会话")
	}
	u, _ := url.Parse(origin)
	c := &Client{
		server: origin, origin: u, secrets: secrets, cookies: make(map[string]sessionCookie),
		http: &http.Client{
			Timeout: 120 * time.Second,
			Transport: &http.Transport{
				Proxy:               http.ProxyFromEnvironment,
				DialContext:         (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	for _, name := range cookieNames {
		data, err := secrets.Get(c.secretKey(name))
		if errors.Is(err, fs.ErrNotExist) || (err == nil && len(data) == 0) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("无法读取系统保存的审核会话: %w", err)
		}
		var cookie sessionCookie
		if err := json.Unmarshal(data, &cookie); err != nil || cookie.Name != name || cookie.Value == "" || cookie.Expires.IsZero() {
			return nil, errors.New("系统保存的审核会话损坏，请清除此服务器的会话后重新登录")
		}
		if err := (&http.Cookie{Name: name, Value: cookie.Value}).Valid(); err != nil {
			return nil, errors.New("系统保存的审核会话格式无效，请清除此服务器的会话后重新登录")
		}
		if !cookie.Expires.After(time.Now()) {
			if err := c.deleteSecret(name); err != nil {
				return nil, err
			}
			continue
		}
		c.cookies[name] = cookie
	}
	return c, nil
}

func (c *Client) Server() string { return c.server }

func (c *Client) SetCSRF(token string) {
	c.mu.Lock()
	c.csrf = token
	c.mu.Unlock()
}

func (c *Client) secretKey(name string) string {
	hash := sha256.Sum256([]byte(c.server))
	return "review-session/" + hex.EncodeToString(hash[:]) + "/" + name
}

func (c *Client) deleteSecret(name string) error {
	err := c.secrets.Delete(c.secretKey(name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("无法移除系统保存的审核会话: %w", err)
	}
	return nil
}

// ClearSession explicitly forgets this server. Normal application shutdown
// must use Close instead, so restarting keeps the same ordinary-member ID.
func (c *Client) ClearSession() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.csrf = ""
	c.cookies = make(map[string]sessionCookie)
	var result error
	for _, name := range cookieNames {
		result = errors.Join(result, c.deleteSecret(name))
	}
	return result
}

func (c *Client) Close() error {
	c.http.CloseIdleConnections()
	return nil
}

func (c *Client) apiURL(path string) (string, error) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\\#") || strings.IndexFunc(path, unicode.IsSpace) >= 0 {
		return "", errors.New("无效的 API 路径")
	}
	u, err := url.Parse(path)
	if err != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" {
		return "", errors.New("无效的 API 路径")
	}
	// Decode before checking segments so encoded traversal cannot leave /api.
	decoded := u.Path
	for n := 0; n < 3; n++ {
		if strings.ContainsAny(decoded, "\\\x00") {
			return "", errors.New("API 路径不能离开审核服务")
		}
		for _, segment := range strings.Split(decoded, "/") {
			if segment == "." || segment == ".." {
				return "", errors.New("API 路径不能离开审核服务")
			}
		}
		next, err := url.PathUnescape(decoded)
		if err != nil || next == decoded {
			break
		}
		decoded = next
	}
	return c.server + "/api" + path, nil
}

func (c *Client) request(ctx context.Context, method, path string, body io.Reader, contentType string, length int64) (*http.Response, error) {
	endpoint, err := c.apiURL(path)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	if length >= 0 {
		req.ContentLength = length
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.mu.Lock()
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	for _, name := range cookieNames {
		cookie, ok := c.cookies[name]
		if ok && (cookie.Expires.IsZero() || cookie.Expires.After(time.Now())) && (!cookie.Secure || c.origin.Scheme == "https") {
			req.AddCookie(&http.Cookie{Name: name, Value: cookie.Value})
		}
	}
	c.mu.Unlock()
	response, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, unconfirmed(method, context.Canceled)
		}
		// net/url errors may contain query parameters; do not expose request URLs
		// or credentials in UI errors or logs.
		return nil, unconfirmed(method, errors.New("无法连接审核服务器，请检查网络、服务器地址和 HTTPS 证书"))
	}
	if err := c.receiveCookies(response.Cookies()); err != nil {
		response.Body.Close()
		return nil, err
	}
	return response, nil
}

func (c *Client) receiveCookies(cookies []*http.Cookie) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, cookie := range cookies {
		if cookie.Name != "review_access" && cookie.Name != "review_session" {
			continue
		}
		if cookie.Domain != "" && !strings.EqualFold(strings.TrimPrefix(cookie.Domain, "."), c.origin.Hostname()) {
			continue
		}
		if cookie.Path != "" && cookie.Path != "/" {
			continue
		}
		if cookie.MaxAge < 0 || cookie.Value == "" || (cookie.MaxAge == 0 && !cookie.Expires.IsZero() && !cookie.Expires.After(time.Now())) {
			delete(c.cookies, cookie.Name)
			if err := c.deleteSecret(cookie.Name); err != nil {
				return err
			}
			continue
		}
		expires := cookie.Expires
		if cookie.MaxAge > 0 {
			expires = time.Now().Add(time.Duration(cookie.MaxAge) * time.Second)
		}
		entry := sessionCookie{Name: cookie.Name, Value: cookie.Value, Expires: expires, Secure: cookie.Secure}
		c.cookies[cookie.Name] = entry
		if expires.IsZero() {
			// Respect a session-only cookie instead of making it persistent.
			if err := c.deleteSecret(cookie.Name); err != nil {
				return err
			}
			continue
		}
		data, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		if len(data) > 2560 {
			return errors.New("审核会话超过系统凭据存储上限，无法安全保存")
		}
		if err := c.secrets.Set(c.secretKey(cookie.Name), data); err != nil {
			return fmt.Errorf("服务器已响应，但无法安全保存审核会话；请检查系统凭据存储: %w", err)
		}
	}
	return nil
}

func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.json(ctx, http.MethodGet, path, nil, out)
}
func (c *Client) Post(ctx context.Context, path string, body any, out any) error {
	return c.json(ctx, http.MethodPost, path, body, out)
}
func (c *Client) Patch(ctx context.Context, path string, body any, out any) error {
	return c.json(ctx, http.MethodPatch, path, body, out)
}
func (c *Client) Put(ctx context.Context, path string, body any, out any) error {
	return c.json(ctx, http.MethodPut, path, body, out)
}
func (c *Client) Delete(ctx context.Context, path string, out any) error {
	return c.json(ctx, http.MethodDelete, path, nil, out)
}

func (c *Client) json(ctx context.Context, method, path string, value any, out any) error {
	var data []byte
	var err error
	contentType := ""
	if value != nil || method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
		if value == nil {
			value = map[string]any{}
		}
		data, err = json.Marshal(value)
		if err != nil {
			return err
		}
		contentType = "application/json"
	}
	response, err := c.request(ctx, method, path, bytes.NewReader(data), contentType, int64(len(data)))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	return unconfirmed(method, c.decode(response, path, out))
}

// A write may have committed before the connection or response decoding failed.
// Do not encourage a second POST merely because its result did not arrive.
func unconfirmed(method string, err error) error {
	if err == nil || method == http.MethodGet || method == http.MethodHead {
		return err
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return err
	}
	return fmt.Errorf("请求结果尚未确认，请先刷新核对后再决定是否重试：%w", err)
}

func (c *Client) decode(response *http.Response, path string, out any) error {
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return responseError(response)
	}
	if response.StatusCode == http.StatusNoContent {
		return nil
	}
	const maxJSON = 64 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, maxJSON+1))
	if err != nil {
		return fmt.Errorf("读取服务器响应失败: %w", err)
	}
	if len(data) > maxJSON {
		return errors.New("服务器响应过大")
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if strings.HasPrefix(path, "/auth/") {
		var auth struct {
			CSRF string `json:"csrf"`
		}
		if json.Unmarshal(data, &auth) == nil && auth.CSRF != "" {
			c.SetCSRF(auth.CSRF)
		}
		if path == "/auth/leave" {
			c.SetCSRF("")
		}
	}
	if out == nil {
		if !json.Valid(data) {
			return errors.New("服务器返回了无效的 JSON")
		}
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return errors.New("服务器响应格式不兼容")
	}
	return nil
}

func responseError(response *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	message := fmt.Sprintf("请求失败（HTTP %d）", response.StatusCode)
	var payload struct {
		Detail json.RawMessage `json:"detail"`
	}
	if json.Unmarshal(data, &payload) == nil {
		var detail string
		if json.Unmarshal(payload.Detail, &detail) == nil && detail != "" {
			message = detail
		} else {
			var details []struct {
				Message  string `json:"msg"`
				Location []any  `json:"loc"`
			}
			if json.Unmarshal(payload.Detail, &details) == nil {
				parts := make([]string, 0, len(details))
				for _, item := range details {
					if item.Message != "" {
						parts = append(parts, item.Message)
					}
				}
				if len(parts) > 0 {
					message = strings.Join(parts, "；")
				}
			}
		}
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		message = "审核服务返回重定向，请直接填写最终 HTTPS 服务器地址"
	}
	return &APIError{Status: response.StatusCode, Message: message}
}
