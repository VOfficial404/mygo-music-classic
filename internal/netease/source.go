package netease

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"mygo-music/internal/music"
)

// ID identifies NetEase tracks and the account's library in the player.
const ID = "netease"

var (
	_ music.Source = (*Source)(nil)
	_ music.Prompter = (*Source)(nil)
	_ music.Recorder = (*Source)(nil)
)

func (s *Source) Info() music.Info {
	return music.Info{ID: ID, Name: "网易云音乐", Playback: music.PlaybackStream, Navigation: []music.NavItem{
		{Builtin: "home", Title: "发现音乐"},
		{Builtin: "liked", Title: "我喜欢的音乐"},
		{Builtin: "stations", Title: "私人 FM"},
		{Page: "toplists", Title: "排行榜", Icon: "chart"},
		{Page: "playlists", Title: "歌单广场", Icon: "playlist"},
		{Page: "cloud", Title: "云盘", Icon: "note"},
		{Builtin: "savedAlbums", Title: "收藏的专辑"},
		{Builtin: "shows", Title: "播客"},
	}}
}

func (s *Source) Can(what string) bool {
	switch what {
	case "signIn", "signOut", "account", "home", "browse", "search", "suggest",
		"playlists", "playlist", "album", "artist", "liked", "recent", "played", "isLiked", "like", "likeEpisode",
		"createPlaylist", "renamePlaylist", "deletePlaylist", "addToPlaylist", "removeFromPlaylist", "movePlaylistTrack", "savePlaylist",
		"followArtist", "saveAlbum", "savedAlbums", "shows", "show", "subscribeShow", "stations", "stationNext",
		"comments", "likeComment", "postComment", "lyrics", "stream":
		return true
	}
	return false
}

func (s *Source) Setting(string) string { return "" }
func (s *Source) SetSetting(string, string) error { return music.ErrUnsupported }

func (s *Source) RemotePlay([]string, int, int64) error { return music.ErrUnsupported }
func (s *Source) RemotePause() error { return music.ErrUnsupported }
func (s *Source) RemoteResume() error { return music.ErrUnsupported }
func (s *Source) RemoteSeek(int64) error { return music.ErrUnsupported }
func (s *Source) RemoteVolume(int) error { return music.ErrUnsupported }
func (s *Source) RemoteState() (*music.RemoteState, error) { return nil, music.ErrUnsupported }
func (s *Source) RemoteDevices() ([]music.Device, error) { return nil, music.ErrUnsupported }
func (s *Source) RemoteUse(string) error { return music.ErrUnsupported }

type loginAttempt struct {
	cancel context.CancelFunc
	done chan struct{}
	account *music.Account
	err error
}

func (s *Source) OnSignInPrompt(show func(music.SignInPrompt)) {
	s.mu.Lock()
	s.prompt = show
	s.mu.Unlock()
}

func (s *Source) signInPrompt(prompt music.SignInPrompt) {
	s.mu.Lock()
	show := s.prompt
	s.mu.Unlock()
	if show != nil {
		show(prompt)
	}
}

func (s *Source) SignIn() (*music.Account, error) {
	s.mu.Lock()
	if attempt := s.login; attempt != nil {
		s.mu.Unlock()
		<-attempt.done
		return cloneAccount(attempt.account), attempt.err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	attempt := &loginAttempt{cancel: cancel, done: make(chan struct{})}
	s.login = attempt
	state := &session{}
	if nmtid, ok := s.current.cookies["NMTID"]; ok {
		state.cookies = map[string]storedCookie{"NMTID": nmtid}
	}
	s.mu.Unlock()

	account, err := s.scanQRCode(ctx, state)
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.login != attempt:
		err = music.Fail(music.ErrSignIn, "本次登录已取消")
	case ctx.Err() == context.DeadlineExceeded:
		err = music.Fail(music.ErrSignIn, "扫码登录超时，请重新登录")
	case err == nil:
		err = s.persistLocked(state)
		if err == nil {
			state.account = account
			s.current = state
		} else {
			err = fmt.Errorf("保存网易云音乐登录状态失败: %w", err)
		}
	}
	if s.login == attempt {
		s.login = nil
	}
	if err == nil {
		attempt.account = account
	}
	attempt.err = err
	close(attempt.done)
	return cloneAccount(attempt.account), err
}

func (s *Source) qrKey(ctx context.Context, state *session) (string, error) {
	var reply struct {
		apiStatus
		Key string `json:"unikey"`
	}
	if err := s.requestOn(ctx, state, desktop, "/api/login/qrcode/unikey", fields{"type": 3}, &reply); err != nil {
		return "", err
	}
	if reply.Key == "" {
		return "", errors.New("网易云音乐未返回登录二维码")
	}
	return reply.Key, nil
}

func (s *Source) scanQRCode(ctx context.Context, state *session) (*music.Account, error) {
	key, err := s.qrKey(ctx, state)
	if err != nil {
		return nil, err
	}
	qr := site + "/login?codekey=" + url.QueryEscape(key)
	s.signInPrompt(music.SignInPrompt{QR: qr, Text: "用网易云音乐 App 扫码"})
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	status := 801
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
		var reply apiStatus
		err := s.requestOn(ctx, state, desktop, "/api/login/qrcode/client/login", fields{"key": key, "type": 3}, &reply, 800, 801, 802, 803)
		if err != nil {
			return nil, err
		}
		switch reply.Code {
		case 800:
			return nil, music.Fail(music.ErrSignIn, "二维码已过期，请重新登录")
		case 801, 802:
			if status != reply.Code {
				text := "用网易云音乐 App 扫码"
				if reply.Code == 802 {
					text = "已扫码，请在手机上确认"
				}
				s.signInPrompt(music.SignInPrompt{QR: qr, Text: text})
				status = reply.Code
			}
		case 803:
			account, err := s.fetchAccount(ctx, state)
			if err != nil {
				return nil, err
			}
			s.mu.Lock()
			signedIn := state.cookie("MUSIC_U", time.Now()) != ""
			s.mu.Unlock()
			if account == nil || !signedIn {
				return nil, music.Fail(music.ErrSignIn, "未取得登录状态，请重新登录")
			}
			return account, nil
		default:
			return nil, apiError{reply.Code}
		}
	}
}

func (s *Source) SignOut() error {
	s.mu.Lock()
	if s.login != nil {
		s.login.cancel()
		s.login = nil
	}
	old := s.current
	signedIn := old.cookie("MUSIC_U", time.Now()) != ""
	s.current = &session{}
	persistErr := s.persistLocked(s.current)
	s.mu.Unlock()
	if !signedIn {
		return persistErr
	}
	logoutErr := s.requestOn(context.Background(), old, web, "/api/logout", nil, nil)
	if errors.Is(logoutErr, music.ErrSignIn) {
		logoutErr = nil
	}
	return errors.Join(persistErr, logoutErr)
}

func (s *Source) Account() (*music.Account, error) {
	state, err := s.session(true)
	if err != nil {
		return nil, nil
	}
	account, err := s.fetchAccount(context.Background(), state)
	s.mu.Lock()
	defer s.mu.Unlock()
	if state != s.current {
		return cloneAccount(s.current.account), nil
	}
	if err != nil && !errors.Is(err, music.ErrSignIn) {
		return nil, err
	}
	if account == nil || state.cookie("MUSIC_U", time.Now()) == "" {
		s.current = &session{}
		return nil, s.persistLocked(s.current)
	}
	state.account = account
	return cloneAccount(account), nil
}

func (s *Source) fetchAccount(ctx context.Context, state *session) (*music.Account, error) {
	var reply struct {
		apiStatus
		Profile remoteUser `json:"profile"`
	}
	if err := s.requestOn(ctx, state, web, "/api/nuser/account/get", nil, &reply); err != nil {
		return nil, err
	}
	if reply.Profile.UserID == 0 {
		return nil, nil
	}
	return reply.Profile.account(), nil
}

func (s *Source) userID(state *session) (int64, error) {
	s.mu.Lock()
	account := state.account
	s.mu.Unlock()
	if account == nil {
		var err error
		account, err = s.fetchAccount(context.Background(), state)
		if err != nil {
			return 0, err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if account == nil || state != s.current || state.cookie("MUSIC_U", time.Now()) == "" {
			return 0, music.ErrSignIn
		}
		state.account = account
	}
	return strconv.ParseInt(account.ID, 10, 64)
}

func cloneAccount(account *music.Account) *music.Account {
	if account == nil {
		return nil
	}
	copy := *account
	return &copy
}
