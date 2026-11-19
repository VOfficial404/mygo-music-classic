package netease

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"mygo-music/internal/music"
)

func (s *Source) Home() (*music.Browse, error) {
	state, signInErr := s.session(true)
	var daily, news []music.Track
	var recommended []music.Playlist
	var podcasts []music.Show
	var newsErr, playlistErr, podcastErr error
	var wg sync.WaitGroup
	if signInErr == nil {
		wg.Go(func() {
			daily, _ = s.dailySongs(state)
		})
	}
	wg.Go(func() { news, newsErr = s.newSongs() })
	wg.Go(func() { recommended, playlistErr = s.recommendedPlaylists(state) })
	wg.Go(func() { podcasts, podcastErr = s.recommendedShows() })
	wg.Wait()
	if newsErr != nil && playlistErr != nil && podcastErr != nil && len(daily) == 0 {
		return nil, errors.Join(newsErr, playlistErr, podcastErr)
	}

	page := &music.Browse{Title: "发现音乐"}
	if len(daily) != 0 {
		page.Sections = append(page.Sections, music.Section{
			Title: "每日推荐", PageTitle: "每日歌曲推荐", Subtitle: "根据你的口味生成，每天 6:00 更新", Tracks: daily,
		})
	}
	if len(recommended) != 0 {
		page.Sections = append(page.Sections, music.Section{Title: "推荐歌单", Playlists: recommended, MorePage: "playlists"})
	}
	links := []music.Link{{Title: "排行榜", Page: "toplists"}, {Title: "热门歌手", Page: "artists"}}
	if signInErr == nil {
		links = append([]music.Link{{Title: "私人雷达", Page: "radar"}}, links...)
	}
	page.Sections = append(page.Sections, music.Section{Links: links})
	if len(news) != 0 {
		page.Sections = append(page.Sections, music.Section{Title: "新歌速递", Tracks: news[:min(len(news), 12)], MorePage: "newsongs"})
	}
	if len(podcasts) != 0 {
		page.Sections = append(page.Sections, music.Section{Title: "推荐播客", Shows: podcasts})
	}
	return page, nil
}

func (s *Source) Browse(page string) (*music.Browse, error) {
	kind, argument, _ := strings.Cut(page, ":")
	switch kind {
	case "user":
		uid, err := parseID(argument)
		if err != nil {
			return nil, err
		}
		return s.userPage(uid)
	case "toplists":
		return s.toplists()
	case "radar":
		if _, err := s.session(true); err != nil {
			return nil, err
		}
		p, err := s.Playlist("3136952023")
		if err != nil {
			return nil, err
		}
		return &music.Browse{Title: "私人雷达", ImageURL: p.ImageURL, Description: p.Description, Sections: []music.Section{{Tracks: p.Tracks}}}, nil
	case "cloud":
		return s.cloud(pageOffset(argument))
	case "newsongs":
		tracks, err := s.newSongs()
		if err != nil {
			return nil, err
		}
		return &music.Browse{Title: "新歌速递", Sections: []music.Section{{Tracks: tracks, Empty: "暂无新歌"}}}, nil
	case "playlists":
		if argument == "" {
			return s.playlistCategories()
		}
		category, offset, _ := strings.Cut(argument, ":")
		return s.categoryPlaylists(category, pageOffset(offset))
	case "artists":
		return s.topArtists(pageOffset(argument))
	default:
		return nil, music.Fail(music.ErrUnavailable, "此页面不存在")
	}
}

func pageOffset(value string) int {
	offset, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return max(0, offset)
}

func morePage(prefix string, offset, count int, more bool) string {
	if !more || count == 0 || offset > int(^uint(0)>>1)-count {
		return ""
	}
	return prefix + ":" + strconv.Itoa(offset+count)
}

func (s *Source) dailySongs(state *session) ([]music.Track, error) {
	var reply struct {
		apiStatus
		Data struct { Songs []remoteSong `json:"dailySongs"` } `json:"data"`
	}
	if err := s.requestOn(context.Background(), state, web, "/api/v3/discovery/recommend/songs", nil, &reply); err != nil {
		return nil, err
	}
	return convertSlice(reply.Data.Songs, remoteSong.track), nil
}

func (s *Source) recommendedPlaylists(state *session) ([]music.Playlist, error) {
	var reply struct {
		apiStatus
		Result []remotePlaylist `json:"result"`
		Personal []remotePlaylist `json:"recommend"`
	}
	if state != nil {
		if err := s.requestOn(context.Background(), state, web, "/api/v1/discovery/recommend/resource", nil, &reply); err == nil && len(reply.Personal) > 0 {
			viewer := s.knownUserID()
			return convertSlice(reply.Personal, func(p remotePlaylist) music.Playlist { return p.playlist(viewer) }), nil
		}
	}
	if err := s.request(web, "/api/personalized/playlist", fields{"limit": 24, "total": true, "n": 1000}, &reply); err != nil {
		return nil, err
	}
	viewer := s.knownUserID()
	return convertSlice(reply.Result, func(p remotePlaylist) music.Playlist { return p.playlist(viewer) }), nil
}

func (s *Source) recommendedShows() ([]music.Show, error) {
	var reply struct {
		apiStatus
		Shows []remoteShow `json:"djRadios"`
	}
	if err := s.request(web, "/api/djradio/recommend/v1", nil, &reply); err != nil {
		return nil, err
	}
	return convertSlice(reply.Shows, remoteShow.show), nil
}

func (s *Source) newSongs() ([]music.Track, error) {
	var reply struct {
		apiStatus
		Songs []remoteSong `json:"data"`
	}
	if err := s.request(web, "/api/v1/discovery/new/songs", fields{"areaId": 0, "total": true}, &reply); err != nil {
		return nil, err
	}
	return convertSlice(reply.Songs, remoteSong.track), nil
}

func (s *Source) toplists() (*music.Browse, error) {
	var reply struct {
		apiStatus
		Lists []remotePlaylist `json:"list"`
	}
	if err := s.request(web, "/api/toplist", nil, &reply); err != nil {
		return nil, err
	}
	viewer := s.knownUserID()
	return &music.Browse{Title: "排行榜", Sections: []music.Section{{Playlists: convertSlice(reply.Lists, func(p remotePlaylist) music.Playlist { return p.playlist(viewer) })}}}, nil
}

func (s *Source) playlistCategories() (*music.Browse, error) {
	var reply struct {
		apiStatus
		Groups map[int]string `json:"categories"`
		Categories []struct {
			Name string `json:"name"`
			Group int `json:"category"`
		} `json:"sub"`
	}
	if err := s.request(desktopRead, "/api/playlist/catalogue", nil, &reply); err != nil {
		return nil, err
	}
	groupIDs := make([]int, 0, len(reply.Groups))
	groupLinks := make(map[int][]music.Link, len(reply.Groups))
	for id := range reply.Groups {
		groupIDs = append(groupIDs, id)
	}
	sort.Ints(groupIDs)
	for _, c := range reply.Categories {
		if c.Name != "" {
			groupLinks[c.Group] = append(groupLinks[c.Group], music.Link{Title: c.Name, Page: "playlists:" + c.Name})
		}
	}
	page := &music.Browse{Title: "歌单广场", Sections: []music.Section{{Links: []music.Link{{Title: "全部歌单", Page: "playlists:全部"}}}}}
	for _, id := range groupIDs {
		if links := groupLinks[id]; len(links) > 0 {
			page.Sections = append(page.Sections, music.Section{Title: reply.Groups[id], Links: links})
		}
	}
	return page, nil
}

func (s *Source) categoryPlaylists(category string, offset int) (*music.Browse, error) {
	var reply struct {
		apiStatus
		Playlists []remotePlaylist `json:"playlists"`
		More bool `json:"more"`
		Total int `json:"total"`
	}
	data := fields{"cat": category, "order": "hot", "limit": pageSize, "offset": offset, "total": true}
	if err := s.request(web, "/api/playlist/list", data, &reply); err != nil {
		return nil, err
	}
	viewer := s.knownUserID()
	lists := convertSlice(reply.Playlists, func(p remotePlaylist) music.Playlist { return p.playlist(viewer) })
	more := reply.More || len(lists) < reply.Total-offset
	return &music.Browse{Title: category + "歌单", Sections: []music.Section{{Playlists: lists, Empty: "暂无歌单", MorePage: morePage("playlists:"+category, offset, len(lists), more)}}}, nil
}

func (s *Source) topArtists(offset int) (*music.Browse, error) {
	var reply struct {
		apiStatus
		Artists []remoteArtist `json:"artists"`
		More bool `json:"more"`
	}
	data := fields{"limit": pageSize, "total": true, "offset": offset}
	if err := s.request(web, "/api/artist/top", data, &reply); err != nil {
		return nil, err
	}
	artists := convertSlice(reply.Artists, remoteArtist.artist)
	return &music.Browse{Title: "热门歌手", Sections: []music.Section{{Artists: artists, MorePage: morePage("artists", offset, len(artists), reply.More)}}}, nil
}

func (s *Source) cloud(offset int) (*music.Browse, error) {
	state, err := s.session(true)
	if err != nil {
		return nil, err
	}
	var reply struct {
		apiStatus
		Songs []struct {
			ID int64 `json:"songId"`
			Name string `json:"songName"`
			Song remoteSong `json:"simpleSong"`
		} `json:"data"`
		More bool `json:"hasMore"`
	}
	data := fields{"limit": pageSize, "offset": offset}
	if err := s.requestOn(context.Background(), state, web, "/api/v1/cloud/get", data, &reply); err != nil {
		return nil, err
	}
	tracks := make([]music.Track, len(reply.Songs))
	for i, row := range reply.Songs {
		if row.ID != 0 {
			row.Song.ID = row.ID
		}
		row.Song.Name = firstText(row.Name, row.Song.Name)
		tracks[i] = row.Song.track()
	}
	return &music.Browse{Title: "云盘", Sections: []music.Section{{Tracks: tracks, Empty: "云盘暂无歌曲", MorePage: morePage("cloud", offset, len(tracks), reply.More)}}}, nil
}

func (s *Source) userPage(uid int64) (*music.Browse, error) {
	state, _ := s.session(false)
	var reply struct {
		apiStatus
		Profile remoteUser `json:"profile"`
	}
	if err := s.requestOn(context.Background(), state, web, "/api/v1/user/detail/"+formatID(uid), nil, &reply); err != nil {
		return nil, err
	}
	if reply.Profile.UserID == 0 {
		return nil, music.Fail(music.ErrUnavailable, "用户不存在或主页不可访问")
	}
	rows, err := s.userPlaylists(state, uid)
	if err != nil {
		return nil, err
	}
	viewer := s.knownUserID()
	created := music.Section{Title: "创建的歌单", Empty: "暂无公开创建的歌单"}
	saved := music.Section{Title: "收藏的歌单", Empty: "暂无公开收藏的歌单"}
	for i, row := range rows {
		p := row.playlist(viewer)
		if row.ownerID() == uid {
			if viewer == uid && i == 0 {
				p.Name, p.Fixed = playlistLikesName, true
			}
			created.Playlists = append(created.Playlists, p)
		} else {
			if viewer == uid {
				p.Saved = true
			}
			saved.Playlists = append(saved.Playlists, p)
		}
	}
	return &music.Browse{
		Title: firstText(reply.Profile.Name, fmt.Sprintf("用户 %d", uid)), ImageURL: picture(reply.Profile.Avatar),
		Description: strings.TrimSpace(reply.Profile.Signature), Sections: []music.Section{created, saved},
	}, nil
}
