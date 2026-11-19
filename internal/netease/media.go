package netease

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"

	"mygo-music/internal/music"
)

func (s *Source) Shows() ([]music.Show, error) {
	state, err := s.session(true)
	if err != nil {
		return nil, err
	}
	var shows []music.Show
	seen := make(map[int64]struct{})
	for offset := 0; ; {
		var reply struct {
			apiStatus
			Shows []remoteShow `json:"djRadios"`
			More bool `json:"hasMore"`
		}
		data := fields{"limit": 100, "offset": offset, "total": true}
		if err := s.requestOn(context.Background(), state, web, "/api/djradio/get/subed", data, &reply); err != nil {
			return nil, err
		}
		start := len(shows)
		for _, row := range reply.Shows {
			if _, ok := seen[row.ID]; !ok {
				seen[row.ID] = struct{}{}
				row.Subscribed = true
				shows = append(shows, row.show())
			}
		}
		if !reply.More {
			return shows, nil
		}
		if len(shows) == start {
			return nil, errors.New("网易云音乐未返回完整的播客订阅列表")
		}
		offset += len(reply.Shows)
	}
}

func (s *Source) Show(id string, offset int) (*music.ShowPage, error) {
	n, err := parseID(id)
	if err != nil {
		return nil, err
	}
	state, _ := s.session(false)
	var detail struct {
		apiStatus
		Show remoteShow `json:"data"`
	}
	if err := s.requestOn(context.Background(), state, web, "/api/djradio/v2/get", fields{"id": n}, &detail); err != nil {
		return nil, err
	}
	if detail.Show.ID == 0 {
		return nil, music.Fail(music.ErrUnavailable, "播客不存在或无权访问")
	}
	var reply struct {
		apiStatus
		Programs []remoteEpisode `json:"programs"`
		Count int `json:"count"`
		More bool `json:"more"`
	}
	offset = max(0, offset)
	data := fields{"radioId": n, "limit": pageSize, "offset": offset, "asc": false}
	if err := s.requestOn(context.Background(), state, web, "/api/dj/program/byradio", data, &reply); err != nil {
		return nil, err
	}
	show := detail.Show.show()
	episodes := convertSlice(reply.Programs, func(p remoteEpisode) music.Track { return p.episode(show) })
	s.likesMu.Lock()
	for _, p := range reply.Programs {
		if p.Liked != nil {
			if state.episodeLikes == nil {
				state.episodeLikes = make(map[int64]bool)
			}
			// A program page may have been requested before a completed toggle.
			if _, known := state.episodeLikes[p.ID]; !known {
				state.episodeLikes[p.ID] = *p.Liked
			}
		}
	}
	s.likesMu.Unlock()
	more := len(episodes) > 0 && (reply.More || len(episodes) < reply.Count-offset)
	return &music.ShowPage{Show: show, Episodes: episodes, More: more}, nil
}

func (s *Source) SubscribeShow(id string, on bool) error {
	n, err := parseID(id)
	if err != nil {
		return err
	}
	return s.mutate(desktop, togglePath("/api/djradio/", "sub", "unsub", on), fields{"id": n})
}

func (s *Source) LikeEpisode(id string, on bool) error {
	_, program, err := trackIDs(id)
	if err != nil {
		return err
	}
	if program == 0 {
		return music.Fail(music.ErrUnavailable, "无效的播客节目 ID")
	}
	state, err := s.session(true)
	if err != nil {
		return err
	}
	s.likesMu.Lock()
	defer s.likesMu.Unlock()
	path := togglePath("/api/resource/", "like", "unlike", on)
	if err := s.requestOn(context.Background(), state, web, path, fields{"threadId": "A_DJ_1_" + formatID(program)}, nil); err != nil {
		return err
	}
	if state.episodeLikes == nil {
		state.episodeLikes = make(map[int64]bool)
	}
	state.episodeLikes[program] = on
	return nil
}

func (s *Source) Stations() ([]music.Station, error) {
	return []music.Station{{ID: "personal-fm", Name: "私人 FM", Description: "根据你的口味持续推荐歌曲"}}, nil
}

func (s *Source) StationNext(id string) ([]music.Track, error) {
	if id != "personal-fm" {
		return nil, music.Fail(music.ErrUnavailable, "电台不存在")
	}
	state, err := s.session(true)
	if err != nil {
		return nil, err
	}
	var reply struct {
		apiStatus
		Songs []remoteSong `json:"data"`
	}
	if err := s.requestOn(context.Background(), state, web, "/api/v1/radio/get", nil, &reply); err != nil {
		return nil, err
	}
	return convertSlice(reply.Songs, remoteSong.track), nil
}

func commentThread(trackID string) (string, int64, error) {
	song, program, err := trackIDs(trackID)
	if err != nil {
		return "", 0, err
	}
	if program != 0 {
		return "A_DJ_1_" + formatID(program), program, nil
	}
	return "R_SO_4_" + formatID(song), song, nil
}

// The app's offset counts displayed comments. Keep the chronological cursor
// separately so popular comments can lead without skipping or repeating one.
type commentSnapshot struct {
	state *session
	thread string
	items []music.Comment
	seen map[int64]struct{}
	first, cursor, total int
	before int64
	more bool
}

func (s *Source) Comments(trackID string, offset int) (*music.CommentPage, error) {
	thread, rid, err := commentThread(trackID)
	if err != nil {
		return nil, err
	}
	offset = max(0, offset)
	state, _ := s.session(false)
	s.commentsMu.Lock()
	defer s.commentsMu.Unlock()
	p := s.commentPage
	if offset == 0 || p == nil || p.thread != thread || p.state != state || offset < p.first {
		p = &commentSnapshot{state: state, thread: thread, seen: make(map[int64]struct{}), more: true}
		s.commentPage = p
	}
	p.discardBefore(offset)
	for p.more && (p.cursor == 0 || p.first+len(p.items)-offset < pageSize) {
		var reply struct {
			apiStatus
			Comments []remoteComment `json:"comments"`
			Hot []remoteComment `json:"hotComments"`
			Total int `json:"total"`
			More bool `json:"more"`
		}
		data := fields{"rid": rid, "limit": pageSize, "offset": p.cursor, "beforeTime": 0}
		// NetEase switches deep comment pages to a timestamp cursor.
		const offsetLimit = 5000
		if p.cursor >= offsetLimit && p.before > 0 {
			data["offset"], data["beforeTime"] = 0, p.before
		}
		if err := s.requestOn(context.Background(), state, web, "/api/v1/resource/comments/"+thread, data, &reply); err != nil {
			return nil, err
		}
		if p.cursor == 0 {
			slices.SortStableFunc(reply.Hot, func(a, b remoteComment) int { return b.Likes - a.Likes })
			p.append(reply.Hot)
		}
		if len(reply.Comments) > 0 {
			before := reply.Comments[len(reply.Comments)-1].Time
			if p.cursor >= offsetLimit && p.before > 0 && before >= p.before {
				return nil, errors.New("网易云音乐未返回下一页评论")
			}
			p.before = before
		}
		p.append(reply.Comments)
		p.cursor += len(reply.Comments)
		p.total = max(p.total, reply.Total, p.first+len(p.items))
		p.discardBefore(offset)
		p.more = reply.More && len(reply.Comments) > 0
	}
	start := min(offset-p.first, len(p.items))
	end := start + min(pageSize, len(p.items)-start)
	return &music.CommentPage{Comments: slices.Clone(p.items[start:end]), Total: p.total, More: end < len(p.items) || p.more}, nil
}

func (p *commentSnapshot) discardBefore(offset int) {
	n := min(offset-p.first, len(p.items))
	if n > 0 {
		remaining := copy(p.items, p.items[n:])
		clear(p.items[remaining:])
		p.items = p.items[:remaining]
		p.first += n
	}
}

func (p *commentSnapshot) append(comments []remoteComment) {
	for _, row := range comments {
		if _, ok := p.seen[row.ID]; !ok {
			p.seen[row.ID] = struct{}{}
			p.items = append(p.items, row.comment())
		}
	}
}

func (s *Source) LikeComment(trackID, commentID string, on bool) error {
	thread, _, err := commentThread(trackID)
	if err != nil {
		return err
	}
	cid, err := parseID(commentID)
	if err != nil {
		return err
	}
	return s.mutate(web, togglePath("/api/v1/comment/", "like", "unlike", on), fields{"threadId": thread, "commentId": cid})
}

func (s *Source) PostComment(trackID, text string) (*music.Comment, error) {
	thread, _, err := commentThread(trackID)
	if err != nil {
		return nil, err
	}
	state, err := s.session(true)
	if err != nil {
		return nil, err
	}
	if text = strings.TrimSpace(text); text == "" {
		return nil, errors.New("请输入评论内容")
	}
	var reply struct {
		apiStatus
		Comment remoteComment `json:"comment"`
	}
	if err := s.requestOn(context.Background(), state, desktop, "/api/resource/comments/add", fields{"threadId": thread, "content": text}, &reply); err != nil {
		return nil, err
	}
	if reply.Comment.ID == 0 {
		return nil, errors.New("网易云音乐未返回已发布的评论，请刷新后查看")
	}
	comment := reply.Comment.comment()
	return &comment, nil
}

type remoteStream struct {
	ID int64 `json:"id"`
	Code int `json:"code"`
	URL string `json:"url"`
	Type string `json:"type"`
	Level string `json:"level"`
	BitRate int `json:"br"`
	Size int64 `json:"size"`
	Gain float64 `json:"gain"`
	Peak float64 `json:"peak"`
	Trial *json.RawMessage `json:"freeTrialInfo"`
}

func (s *Source) Stream(id, quality string) (*music.Stream, error) {
	n, _, err := trackIDs(id)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, music.Fail(music.ErrUnavailable, "此播客节目没有可播放的音频")
	}
	level, encoding := "standard", "mp3"
	switch quality {
	case "high":
		level = "exhigh"
	case "lossless":
		level, encoding = "lossless", "flac"
	}
	var reply struct {
		apiStatus
		Data []remoteStream `json:"data"`
	}
	data := fields{"ids": "[" + strconv.FormatInt(n, 10) + "]", "level": level, "encodeType": encoding}
	if err := s.request(desktopRead, "/api/song/enhance/player/url/v1", data, &reply); err != nil {
		return nil, err
	}
	for _, row := range reply.Data {
		if row.ID == n {
			return row.stream()
		}
	}
	return nil, music.Fail(music.ErrUnavailable, "网易云音乐未提供此歌曲的播放地址")
}

func (r remoteStream) stream() (*music.Stream, error) {
	if r.URL == "" || (r.Code != 0 && r.Code != 200) || r.Trial != nil {
		return nil, music.Fail(music.ErrUnavailable, "当前账号无完整播放权限，或此歌曲在当前地区不可用")
	}
	u, err := url.Parse(r.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, music.Fail(music.ErrUnavailable, "网易云音乐返回了无效播放地址")
	}
	typ := strings.ToLower(firstText(r.Type, strings.TrimPrefix(path.Ext(u.Path), ".")))
	if typ == "mp4" || typ == "aac" && strings.HasSuffix(strings.ToLower(u.Path), ".m4a") {
		typ = "m4a"
	}
	switch typ {
	case "mp3", "flac", "ogg", "wav", "m4a", "aac":
	default:
		return nil, music.Fail(music.ErrUnavailable, "播放器暂不支持此音频格式")
	}
	quality := "standard"
	switch {
	case typ == "flac" || r.Level == "lossless" || r.Level == "hires":
		quality = "lossless"
	case r.Level == "higher" || r.Level == "exhigh" || r.BitRate >= 192000:
		quality = "high"
	}
	return &music.Stream{
		URL: r.URL, Type: typ, Quality: quality, Size: r.Size, BitRate: r.BitRate, Gain: r.Gain, Peak: r.Peak,
		Headers: map[string]string{"Referer": site + "/", "User-Agent": desktopUA},
	}, nil
}
