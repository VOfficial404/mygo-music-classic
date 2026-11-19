package netease

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mygo-music/internal/music"
)

const (
	site = "https://music.163.com"
	desktopHost = "https://interfacepc.music.163.com"
	desktopVersion = "3.1.17.204416"
	desktopUA = "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.164 Safari/537.36 NeteaseMusicDesktop/" + desktopVersion
)

type protocol uint8

const (
	web protocol = iota
	desktop
	desktopRead
)

type fields map[string]any

type apiStatus struct {
	Code int `json:"code"`
}

func (r *apiStatus) status() *apiStatus { return r }

type apiResponse interface{ status() *apiStatus }

type apiError struct{ code int }

func (e apiError) Error() string {
	switch e.code {
	case -460, 460, 8821:
		return fmt.Sprintf("网易云音乐暂时拒绝了此请求（%d），请稍后重试", e.code)
	default:
		return fmt.Sprintf("网易云音乐请求失败（%d）", e.code)
	}
}

func (e apiError) risk() bool { return e.code == -460 || e.code == 460 || e.code == 8821 }

func riskError(err error) bool {
	var apiErr apiError
	return errors.As(err, &apiErr) && apiErr.risk()
}

type storedCookie struct {
	Value   string    `json:"value"`
	Expires time.Time `json:"expires,omitempty"`
}

type storedSession struct {
	DeviceID string                  `json:"deviceId"`
	Cookies  map[string]storedCookie `json:"cookies,omitempty"`
}

type session struct {
	// Cookies and account are guarded by Source.mu. The remaining fields are
	// guarded by Source.likesMu and belong only to this account session.
	cookies map[string]storedCookie
	account *music.Account
	likedIDs []int64
	likedSet map[int64]struct{}
	likedUntil time.Time
	episodeLikes map[int64]bool
}

func (state *session) cookie(name string, now time.Time) string {
	entry := state.cookies[name]
	if !entry.Expires.IsZero() && !now.Before(entry.Expires) {
		return ""
	}
	return entry.Value
}

// Source is the built-in NetEase Cloud Music source.
type Source struct {
	client *http.Client
	store string
	deviceID string
	requestID atomic.Uint32

	mu sync.Mutex
	current *session
	login *loginAttempt
	prompt func(music.SignInPrompt)

	likesMu sync.Mutex
	playlistMu sync.Mutex
	commentsMu sync.Mutex
	commentPage *commentSnapshot
}

// New opens the source's local session without contacting NetEase.
func New(dataDir string) *Source {
	s := &Source{
		client: &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		store: filepath.Join(dataDir, "session.json"),
	}
	_ = os.MkdirAll(dataDir, 0700)
	var saved storedSession
	if data, err := os.ReadFile(s.store); err == nil {
		if err := json.Unmarshal(data, &saved); err != nil {
			saved = storedSession{}
		}
	}
	s.deviceID = saved.DeviceID
	if s.deviceID == "" {
		var random [26]byte
		rand.Read(random[:])
		s.deviceID = strings.ToUpper(hex.EncodeToString(random[:]))
	}
	s.current = &session{cookies: saved.Cookies}
	if saved.DeviceID == "" {
		_ = s.persistLocked(s.current)
	}
	return s
}

func (s *Source) session(auth bool) (*session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if auth && s.current.cookie("MUSIC_U", time.Now()) == "" {
		return nil, music.ErrSignIn
	}
	return s.current, nil
}

func (s *Source) persistLocked(state *session) error {
	b, err := json.Marshal(storedSession{DeviceID: s.deviceID, Cookies: state.cookies})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.store), ".netease-session-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(b)
	if err := errors.Join(writeErr, tmp.Close()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.store)
}

func (s *Source) collectCookies(state *session, cookies []*http.Cookie) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	changed := false
	for _, cookie := range cookies {
		// API calls span sibling hosts. Keep only session cookies shared by
		// the first-party API instead of forwarding arbitrary web cookies.
		switch cookie.Name {
		case "MUSIC_U", "MUSIC_A", "__csrf", "NMTID", "MUSIC_R_T":
		default:
			continue
		}
		if cookie.MaxAge < 0 || cookie.Value == "" || (cookie.MaxAge == 0 && !cookie.Expires.IsZero() && !now.Before(cookie.Expires)) {
			if _, ok := state.cookies[cookie.Name]; ok {
				delete(state.cookies, cookie.Name)
				changed = true
			}
			continue
		}
		expires := cookie.Expires
		if cookie.MaxAge > 0 && (expires.IsZero() || !now.Before(expires)) {
			expires = now.Add(time.Duration(cookie.MaxAge) * time.Second).Truncate(time.Second)
		}
		entry := storedCookie{Value: cookie.Value, Expires: expires}
		if state.cookies[cookie.Name] != entry {
			if state.cookies == nil {
				state.cookies = make(map[string]storedCookie)
			}
			state.cookies[cookie.Name] = entry
			changed = true
		}
	}
	if changed && state == s.current {
		return s.persistLocked(state)
	}
	return nil
}

func (s *Source) request(mode protocol, path string, data fields, out apiResponse) error {
	state, _ := s.session(false)
	return s.requestOn(context.Background(), state, mode, path, data, out)
}

func (s *Source) requestOn(ctx context.Context, state *session, mode protocol, path string, data fields, out apiResponse, allowed ...int) error {
	if out == nil {
		out = &apiStatus{}
	}
	err := s.send(ctx, state, mode, path, data, out, allowed)
	// Retry only read requests the service explicitly rejected. A rejection
	// can supply a fresh NMTID for the same account and device.
	if mode == desktopRead && riskError(err) {
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return s.send(ctx, state, mode, path, data, out, allowed)
		}
	}
	return err
}

func (s *Source) send(ctx context.Context, state *session, mode protocol, path string, data fields, out apiResponse, allowed []int) error {
	now := time.Now()
	header := map[string]string{
		"os": "pc", "appver": desktopVersion, "deviceId": s.deviceID,
		"osver": "Microsoft-Windows-10-Professional-build-19045-64bit",
		"versioncode": "140", "channel": "netease", "resolution": "1920x1080",
		"buildver": strconv.FormatInt(now.Unix(), 10),
		"requestId": fmt.Sprintf("%d_%04d", now.UnixMilli(), s.requestID.Add(1)%10000),
	}
	s.mu.Lock()
	header["__csrf"] = state.cookie("__csrf", now)
	for name, entry := range state.cookies {
		if entry.Expires.IsZero() || now.Before(entry.Expires) {
			header[name] = entry.Value
		}
	}
	s.mu.Unlock()

	body := make(fields, len(data)+3)
	for name, value := range data {
		body[name] = value
	}
	body["csrf_token"] = header["__csrf"]
	if mode != web {
		body["e_r"] = false
		body["header"] = header
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	var endpoint, encoded string
	if mode == web {
		endpoint = site + "/weapi/" + strings.TrimPrefix(path, "/api/")
		encoded = weapi(payload, weapiSecret()).Encode()
	} else {
		host := desktopHost
		if path == "/api/feedback/weblog" {
			host = "https://clientlog.music.163.com"
		}
		endpoint = host + "/eapi/" + strings.TrimPrefix(path, "/api/")
		encoded = "params=" + eapi(path, payload)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(encoded))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", desktopUA)
	req.Header.Set("Referer", site+"/")
	req.Header.Set("Origin", site)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for name, value := range header {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("连接网易云音乐失败: %w", err)
	}
	defer resp.Body.Close()
	if err := s.collectCookies(state, resp.Cookies()); err != nil {
		return fmt.Errorf("保存网易云音乐登录状态失败: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return music.ErrSignIn
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("网易云音乐请求失败（HTTP %d）", resp.StatusCode)
	}
	const responseLimit = 32 << 20
	out.status().Code = 0
	if err := json.NewDecoder(io.LimitReader(resp.Body, responseLimit)).Decode(out); err != nil {
		return fmt.Errorf("无法读取网易云音乐的响应: %w", err)
	}
	code := out.status().Code
	for _, acceptable := range allowed {
		if code == acceptable {
			return nil
		}
	}
	switch code {
	case 200:
		return nil
	case 301, 302:
		return music.ErrSignIn
	case 401:
		if header["MUSIC_U"] == "" {
			return music.ErrSignIn
		}
		return music.Fail(music.ErrUnavailable, "当前账号无权访问该内容")
	case 404:
		return music.Fail(music.ErrUnavailable, "此内容不存在或不可用")
	default:
		return apiError{code}
	}
}
