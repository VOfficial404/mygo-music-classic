package netease

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"mygo-music/internal/music"
)

const (
	pageSize = 50
	likedPageSize = 100
	detailBatchSize = 500
)

func (s *Source) Search(query string, kind music.SearchKind, offset int) (*music.SearchResult, error) {
	var queryType int
	switch kind {
	case music.SearchTracks:
		queryType = 1
	case music.SearchAlbums:
		queryType = 10
	case music.SearchArtists:
		queryType = 100
	case music.SearchPlaylists:
		queryType = 1000
	case music.SearchShows:
		queryType = 1009
	default:
		return nil, music.ErrUnsupported
	}
	if query = strings.TrimSpace(query); query == "" {
		return &music.SearchResult{}, nil
	}
	offset = max(offset, 0)
	var reply struct {
		apiStatus
		Result struct {
			Songs []remoteSong `json:"songs"`
			Albums []remoteAlbum `json:"albums"`
			Artists []remoteArtist `json:"artists"`
			Playlists []remotePlaylist `json:"playlists"`
			Shows []remoteShow `json:"djRadios"`
			SongCount int `json:"songCount"`
			AlbumCount int `json:"albumCount"`
			ArtistCount int `json:"artistCount"`
			PlaylistCount int `json:"playlistCount"`
			ShowCount int `json:"djRadiosCount"`
			HasMore bool `json:"hasMore"`
		} `json:"result"`
	}
	path, protocol := "/api/search/get", desktopRead
	if kind == music.SearchTracks {
		path, protocol = "/api/cloudsearch/pc", web
	}
	if err := s.request(protocol, path, fields{"s": query, "type": queryType, "offset": offset, "limit": pageSize, "total": true}, &reply); err != nil {
		return nil, err
	}
	rows := &reply.Result
	result := &music.SearchResult{}
	var count int
	switch kind {
	case music.SearchTracks:
		result.Tracks, count = convertSlice(rows.Songs, remoteSong.track), rows.SongCount
	case music.SearchAlbums:
		result.Albums, count = convertSlice(rows.Albums, remoteAlbum.album), rows.AlbumCount
	case music.SearchArtists:
		result.Artists, count = convertSlice(rows.Artists, remoteArtist.artist), rows.ArtistCount
	case music.SearchPlaylists:
		viewer := s.knownUserID()
		result.Playlists, count = convertSlice(rows.Playlists, func(p remotePlaylist) music.Playlist { return p.playlist(viewer) }), rows.PlaylistCount
	case music.SearchShows:
		result.Shows, count = convertSlice(rows.Shows, remoteShow.show), rows.ShowCount
	}
	result.More = result.Len() > 0 && (rows.HasMore || result.Len() < count-offset)
	return result, nil
}

func (s *Source) Suggest(query string) ([]string, error) {
	if query = strings.TrimSpace(query); query == "" {
		return nil, nil
	}
	var reply struct {
		apiStatus
		Result struct {
			Matches []struct { Keyword string `json:"keyword"` } `json:"allMatch"`
		} `json:"result"`
	}
	if err := s.request(web, "/api/search/suggest/keyword", fields{"s": query}, &reply); err != nil {
		return nil, err
	}
	result := make([]string, 0, min(len(reply.Result.Matches), 10))
	for _, match := range reply.Result.Matches {
		if match.Keyword != "" && !slices.Contains(result, match.Keyword) {
			result = append(result, match.Keyword)
			if len(result) == 10 {
				break
			}
		}
	}
	return result, nil
}

func (s *Source) knownUserID() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current.account == nil || s.current.cookie("MUSIC_U", time.Now()) == "" {
		return 0
	}
	id, _ := parseID(s.current.account.ID)
	return id
}

func (s *Source) userPlaylists(state *session, uid int64) ([]remotePlaylist, error) {
	var result []remotePlaylist
	seen := make(map[int64]struct{})
	for offset := 0; ; {
		var reply struct {
			apiStatus
			Playlists []remotePlaylist `json:"playlist"`
			More bool `json:"more"`
		}
		data := fields{"uid": uid, "offset": offset, "limit": 200, "includeVideo": false}
		if err := s.requestOn(context.Background(), state, web, "/api/user/playlist", data, &reply); err != nil {
			return nil, err
		}
		start := len(result)
		for _, row := range reply.Playlists {
			if _, exists := seen[row.ID]; !exists {
				seen[row.ID] = struct{}{}
				result = append(result, row)
			}
		}
		if !reply.More {
			return result, nil
		}
		if len(result) == start {
			return nil, errors.New("网易云音乐未返回完整的歌单列表")
		}
		offset += len(reply.Playlists)
	}
}

func (s *Source) Playlists() ([]music.Playlist, error) {
	state, err := s.session(true)
	if err != nil {
		return nil, err
	}
	uid, err := s.userID(state)
	if err != nil {
		return nil, err
	}
	rows, err := s.userPlaylists(state, uid)
	if err != nil {
		return nil, err
	}
	playlists := make([]music.Playlist, len(rows))
	for i, row := range rows {
		p := row.playlist(uid)
		if i == 0 {
			p.Name, p.Mine, p.Fixed, p.Saved = playlistLikesName, true, true, false
		} else {
			p.Saved = !p.Mine
		}
		playlists[i] = p
	}
	return playlists, nil
}

func (s *Source) rawPlaylist(state *session, id int64) (*remotePlaylist, error) {
	var reply struct {
		apiStatus
		Playlist *remotePlaylist `json:"playlist"`
	}
	data := fields{"id": id, "n": 1, "s": 0}
	if err := s.requestOn(context.Background(), state, web, "/api/v6/playlist/detail", data, &reply); err != nil {
		return nil, err
	}
	if reply.Playlist == nil || reply.Playlist.ID == 0 {
		return nil, music.Fail(music.ErrUnavailable, "歌单不存在或无权访问")
	}
	if reply.Playlist.Count > max(len(reply.Playlist.TrackIDs), len(reply.Playlist.Tracks)) {
		data["n"] = 100000
		reply.Playlist = nil
		if err := s.requestOn(context.Background(), state, web, "/api/v6/playlist/detail", data, &reply); err != nil {
			return nil, err
		}
		if reply.Playlist == nil || reply.Playlist.Count > max(len(reply.Playlist.TrackIDs), len(reply.Playlist.Tracks)) {
			return nil, errors.New("网易云音乐未返回歌单的完整歌曲列表")
		}
	}
	return reply.Playlist, nil
}

func (p *remotePlaylist) ids() []int64 {
	if len(p.TrackIDs) >= len(p.Tracks) {
		return convertSlice(p.TrackIDs, func(row songID) int64 { return row.ID })
	}
	return convertSlice(p.Tracks, func(row remoteSong) int64 { return row.ID })
}

func (s *Source) Playlist(id string) (*music.Playlist, error) {
	pid, err := parseID(id)
	if err != nil {
		return nil, err
	}
	state, _ := s.session(false)
	raw, err := s.rawPlaylist(state, pid)
	if err != nil {
		return nil, err
	}
	p := raw.playlist(s.knownUserID())
	p.Tracks, err = s.songDetails(state, raw.ids())
	if err != nil {
		return nil, err
	}
	if p.Fixed {
		p.Name = playlistLikesName
	}
	p.TrackCount = max(p.TrackCount, len(p.Tracks))
	return &p, nil
}

func jsonText(v any) string {
	// Only internal integer IDs and playback log records are encoded here.
	b, _ := json.Marshal(v)
	return string(b)
}

func (s *Source) songDetails(state *session, ids []int64) ([]music.Track, error) {
	all := make([]music.Track, 0, len(ids))
	for batch := range slices.Chunk(ids, detailBatchSize) {
		var reply struct {
			apiStatus
			Songs []remoteSong `json:"songs"`
			Privileges []remotePrivilege `json:"privileges"`
		}
		data := fields{"c": jsonText(convertSlice(batch, func(id int64) songID { return songID{ID: id} }))}
		if err := s.requestOn(context.Background(), state, web, "/api/v3/song/detail", data, &reply); err != nil {
			return nil, err
		}
		privileges := indexPrivileges(reply.Privileges)
		byID := make(map[int64]int, len(reply.Songs))
		for i, song := range reply.Songs {
			byID[song.ID] = i
		}
		for _, id := range batch {
			if i, ok := byID[id]; ok {
				song := reply.Songs[i]
				if status := privileges[id]; status != nil {
					song.Privilege = status
				}
				all = append(all, song.track())
			} else {
				// The playlist still references this ID after removal from the catalog.
				all = append(all, (remoteSong{ID: id, Name: "歌曲暂不可用", Status: -1}).track())
			}
		}
	}
	return all, nil
}

func (s *Source) Album(id string) (*music.AlbumPage, error) {
	if _, err := parseID(id); err != nil {
		return nil, err
	}
	var reply struct {
		apiStatus
		Album *remoteAlbum `json:"album"`
		Songs []remoteSong `json:"songs"`
		Privileges []remotePrivilege `json:"privileges"`
	}
	if err := s.request(web, "/api/v1/album/"+id, nil, &reply); err != nil {
		return nil, err
	}
	if reply.Album == nil {
		return nil, music.Fail(music.ErrUnavailable, "专辑不存在或无权访问")
	}
	return &music.AlbumPage{Album: reply.Album.album(), Tracks: convertTracks(reply.Songs, reply.Privileges)}, nil
}

func (s *Source) Artist(id string) (*music.ArtistPage, error) {
	if _, err := parseID(id); err != nil {
		return nil, err
	}
	state, _ := s.session(false)
	var reply struct {
		apiStatus
		Artist *remoteArtist `json:"artist"`
		Songs []remoteSong `json:"hotSongs"`
		Privileges []remotePrivilege `json:"privileges"`
	}
	if err := s.requestOn(context.Background(), state, web, "/api/v1/artist/"+id, nil, &reply); err != nil {
		return nil, err
	}
	if reply.Artist == nil {
		return nil, music.Fail(music.ErrUnavailable, "歌手不存在或无权访问")
	}
	page := &music.ArtistPage{Artist: reply.Artist.artist(), Tracks: convertTracks(reply.Songs[:min(len(reply.Songs), 50)], reply.Privileges)}
	seen := make(map[int64]struct{})
	for offset := 0; ; {
		var albums struct {
			apiStatus
			Albums []remoteAlbum `json:"hotAlbums"`
			More bool `json:"more"`
		}
		data := fields{"limit": 100, "offset": offset, "total": true}
		if err := s.requestOn(context.Background(), state, web, "/api/artist/albums/"+id, data, &albums); err != nil {
			return nil, err
		}
		before := len(page.Albums)
		for _, a := range albums.Albums {
			if _, ok := seen[a.ID]; !ok {
				seen[a.ID] = struct{}{}
				page.Albums = append(page.Albums, a.album())
			}
		}
		if !albums.More {
			return page, nil
		}
		if len(page.Albums) == before {
			return nil, errors.New("网易云音乐未返回完整的歌手专辑列表")
		}
		offset += len(albums.Albums)
	}
}

func (s *Source) loadLikes(state *session) error {
	if time.Now().Before(state.likedUntil) {
		return nil
	}
	uid, err := s.userID(state)
	if err != nil {
		return err
	}
	var reply struct {
		apiStatus
		IDs []int64 `json:"ids"`
	}
	if err := s.requestOn(context.Background(), state, desktopRead, "/api/song/like/get", fields{"uid": uid}, &reply); err != nil {
		return err
	}
	state.likedIDs = reply.IDs
	state.likedSet = make(map[int64]struct{}, len(reply.IDs))
	for _, id := range reply.IDs {
		state.likedSet[id] = struct{}{}
	}
	state.likedUntil = time.Now().Add(time.Minute)
	return nil
}

func (s *Source) Liked(offset int) (*music.Page, error) {
	state, err := s.session(true)
	if err != nil {
		return nil, err
	}
	s.likesMu.Lock()
	if err := s.loadLikes(state); err != nil {
		s.likesMu.Unlock()
		return nil, err
	}
	offset = min(max(offset, 0), len(state.likedIDs))
	end := offset + min(likedPageSize, len(state.likedIDs)-offset)
	ids, more := state.likedIDs[offset:end], end < len(state.likedIDs)
	s.likesMu.Unlock()
	tracks, err := s.songDetails(state, ids)
	if err != nil {
		return nil, err
	}
	return &music.Page{Tracks: tracks, More: more}, nil
}

func (s *Source) IsLiked(ids []string) (map[string]bool, error) {
	result := make(map[string]bool, len(ids))
	state, err := s.session(true)
	if err != nil {
		for _, id := range ids {
			result[id] = false
		}
		return result, nil
	}
	s.likesMu.Lock()
	defer s.likesMu.Unlock()
	for _, id := range ids {
		song, program, err := trackIDs(id)
		if err != nil {
			result[id] = false
		} else if program != 0 {
			result[id] = state.episodeLikes[program]
		} else {
			if err := s.loadLikes(state); err != nil {
				return nil, err
			}
			_, result[id] = state.likedSet[song]
		}
	}
	return result, nil
}

func (s *Source) Like(id string, on bool) error {
	n, err := parseID(id)
	if err != nil {
		return err
	}
	state, err := s.session(true)
	if err != nil {
		return err
	}
	s.likesMu.Lock()
	defer s.likesMu.Unlock()
	data := fields{"trackId": n, "like": on, "alg": "itembased", "time": "3"}
	if err := s.requestOn(context.Background(), state, web, "/api/radio/like", data, nil); err != nil {
		return err
	}
	state.likedUntil = time.Time{}
	return nil
}

func (s *Source) Recent() ([]music.Track, error) {
	state, err := s.session(true)
	if err != nil {
		return nil, err
	}
	var reply struct {
		apiStatus
		Data struct {
			List []struct { Song remoteSong `json:"data"` } `json:"list"`
		} `json:"data"`
	}
	if err := s.requestOn(context.Background(), state, web, "/api/play-record/song/list", fields{"limit": 100}, &reply); err != nil {
		return nil, err
	}
	tracks := make([]music.Track, 0, len(reply.Data.List))
	for _, row := range reply.Data.List {
		if row.Song.ID != 0 {
			tracks = append(tracks, row.Song.track())
		}
	}
	return tracks, nil
}

func (s *Source) Played(track music.Track) {
	id, program, err := trackIDs(track.ID)
	if err != nil || id == 0 || program != 0 {
		return
	}
	state, err := s.session(true)
	if err != nil {
		return
	}
	logs := []fields{{"action": "startplay", "json": fields{"id": id, "type": "song", "mainsite": "1", "mainsiteWeb": "1"}}}
	_ = s.requestOn(context.Background(), state, desktop, "/api/feedback/weblog", fields{"logs": jsonText(logs)}, nil)
}

func (s *Source) mutate(mode protocol, path string, data fields) error {
	state, err := s.session(true)
	if err != nil {
		return err
	}
	return s.requestOn(context.Background(), state, mode, path, data, nil)
}

func (s *Source) CreatePlaylist(name string) (*music.Playlist, error) {
	state, err := s.session(true)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("请输入歌单名称")
	}
	var reply struct {
		apiStatus
		ID int64 `json:"id"`
		Playlist remotePlaylist `json:"playlist"`
	}
	if err := s.requestOn(context.Background(), state, desktop, "/api/playlist/create", fields{"name": name, "privacy": "0", "type": "NORMAL"}, &reply); err != nil {
		return nil, err
	}
	p := reply.Playlist.playlist(0)
	p.ID, p.Name, p.Mine, p.Saved = firstText(p.ID, formatID(reply.ID)), firstText(p.Name, name), true, false
	if p.ID == "" {
		return nil, errors.New("网易云音乐未返回新建歌单的 ID")
	}
	return &p, nil
}

func (s *Source) RenamePlaylist(id, name string) error {
	pid, err := parseID(id)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("请输入歌单名称")
	}
	return s.mutate(desktop, "/api/playlist/update/name", fields{"id": pid, "name": name})
}

func (s *Source) DeletePlaylist(id string) error {
	pid, err := parseID(id)
	if err != nil {
		return err
	}
	return s.mutate(web, "/api/playlist/remove", fields{"ids": jsonText([]int64{pid})})
}

func (s *Source) AddToPlaylist(id string, trackIDs []string) error {
	return s.changePlaylistTracks(id, trackIDs, "add")
}

func (s *Source) RemoveFromPlaylist(id string, trackIDs []string) error {
	return s.changePlaylistTracks(id, trackIDs, "del")
}

func (s *Source) changePlaylistTracks(id string, tracks []string, op string) error {
	pid, err := parseID(id)
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(tracks))
	seen := make(map[int64]struct{}, len(tracks))
	for _, id := range tracks {
		if strings.HasPrefix(id, "dj:") {
			return music.Fail(music.ErrUnsupported, "歌曲歌单不支持播客节目")
		}
		n, err := parseID(id)
		if err != nil {
			return err
		}
		if _, ok := seen[n]; !ok {
			seen[n] = struct{}{}
			ids = append(ids, n)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	s.playlistMu.Lock()
	defer s.playlistMu.Unlock()
	return s.mutate(web, "/api/playlist/manipulate/tracks", fields{"pid": pid, "trackIds": jsonText(ids), "op": op, "imme": "true"})
}

func (s *Source) MovePlaylistTrack(id string, from, to int) error {
	pid, err := parseID(id)
	if err != nil {
		return err
	}
	s.playlistMu.Lock()
	defer s.playlistMu.Unlock()
	state, err := s.session(true)
	if err != nil {
		return err
	}
	raw, err := s.rawPlaylist(state, pid)
	if err != nil {
		return err
	}
	ids := raw.ids()
	if from < 0 || to < 0 || from >= len(ids) || to >= len(ids) {
		return errors.New("歌曲位置无效，请刷新歌单")
	}
	if from == to {
		return nil
	}
	moved := ids[from]
	if from < to {
		copy(ids[from:to], ids[from+1:to+1])
	} else {
		copy(ids[to+1:from+1], ids[to:from])
	}
	ids[to] = moved
	return s.requestOn(context.Background(), state, desktop, "/api/playlist/manipulate/tracks", fields{"pid": pid, "trackIds": jsonText(ids), "op": "update"}, nil)
}

func (s *Source) SavePlaylist(id string, on bool) error {
	pid, err := parseID(id)
	if err != nil {
		return err
	}
	return s.mutate(desktop, togglePath("/api/playlist/", "subscribe", "unsubscribe", on), fields{"id": pid})
}

func togglePath(prefix, on, off string, enabled bool) string {
	if enabled {
		return prefix + on
	}
	return prefix + off
}

func (s *Source) FollowArtist(id string, on bool) error {
	n, err := parseID(id)
	if err != nil {
		return err
	}
	return s.mutate(web, togglePath("/api/artist/", "sub", "unsub", on), fields{"artistId": n, "artistIds": jsonText([]int64{n})})
}

func (s *Source) SaveAlbum(id string, on bool) error {
	n, err := parseID(id)
	if err != nil {
		return err
	}
	return s.mutate(web, togglePath("/api/album/", "sub", "unsub", on), fields{"id": n})
}

func (s *Source) SavedAlbums(offset int) (*music.AlbumList, error) {
	state, err := s.session(true)
	if err != nil {
		return nil, err
	}
	var reply struct {
		apiStatus
		Albums []remoteAlbum `json:"data"`
		Count int `json:"count"`
		More bool `json:"hasMore"`
	}
	offset = max(offset, 0)
	data := fields{"offset": offset, "limit": pageSize, "total": true}
	if err := s.requestOn(context.Background(), state, web, "/api/album/sublist", data, &reply); err != nil {
		return nil, err
	}
	albums := convertSlice(reply.Albums, func(a remoteAlbum) music.Album {
		a.Saved = true
		return a.album()
	})
	return &music.AlbumList{Albums: albums, More: len(albums) > 0 && (reply.More || len(albums) < reply.Count-offset)}, nil
}
