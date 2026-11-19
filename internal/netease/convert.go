package netease

import (
	"net/url"
	"strconv"
	"strings"
	"time"

	"mygo-music/internal/music"
)

const playlistLikesName = "我喜欢的音乐"

func formatID(id int64) string {
	if id <= 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

func parseID(id string) (int64, error) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n <= 0 || id[0] == '+' {
		return 0, music.Fail(music.ErrUnavailable, "无效的网易云音乐资源 ID")
	}
	return n, nil
}

func firstText(values ...string) string {
	for _, text := range values {
		if text != "" {
			return text
		}
	}
	return ""
}

func picture(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return raw
	}
	u.Scheme = "https"
	host := strings.ToLower(u.Hostname())
	if strings.HasPrefix(host, "p") && strings.HasSuffix(host, ".music.126.net") {
		fragment := u.EscapedFragment()
		u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = "", false, "", ""
		resized := u.String() + "?param={size}y{size}"
		if fragment != "" {
			return resized + "#" + fragment
		}
		return resized
	}
	return u.String()
}

func date(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.DateOnly)
}

func convertSlice[T, U any](from []T, convert func(T) U) []U {
	if len(from) == 0 {
		return nil
	}
	to := make([]U, len(from))
	for i, item := range from {
		to[i] = convert(item)
	}
	return to
}

type remoteUser struct {
	UserID int64 `json:"userId"`
	Name string `json:"nickname"`
	Avatar string `json:"avatarUrl"`
	Signature string `json:"signature"`
}

func (u remoteUser) account() *music.Account {
	id := formatID(u.UserID)
	return &music.Account{ID: id, Name: firstText(u.Name, "网易云用户"), ImageURL: picture(u.Avatar), Page: "user:" + id}
}

type remoteArtist struct {
	ID int64 `json:"id"`
	Name string `json:"name"`
	Picture string `json:"picUrl"`
	Portrait string `json:"img1v1Url"`
	Followed bool `json:"followed"`
}

func (a remoteArtist) artist() music.Artist {
	return music.Artist{ID: formatID(a.ID), Name: a.Name, ImageURL: picture(firstText(a.Picture, a.Portrait)), Following: a.Followed}
}

type remoteAlbum struct {
	ID int64 `json:"id"`
	Name string `json:"name"`
	Picture string `json:"picUrl"`
	Cover string `json:"coverUrl"`
	Blurred string `json:"blurPicUrl"`
	Artists []remoteArtist `json:"artists"`
	Artist *remoteArtist `json:"artist"`
	Published int64 `json:"publishTime"`
	Saved bool `json:"subscribed"`
	IsSub bool `json:"isSub"`
	Info *struct { IsSub bool `json:"isSub"` } `json:"info"`
}

func (a remoteAlbum) album() music.Album {
	artists := convertSlice(a.Artists, remoteArtist.artist)
	if len(artists) == 0 && a.Artist != nil {
		artists = []music.Artist{a.Artist.artist()}
	}
	return music.Album{
		ID: formatID(a.ID), Name: a.Name, ImageURL: picture(firstText(a.Picture, a.Cover, a.Blurred)),
		Artists: artists, Released: date(a.Published), Saved: a.Saved || a.IsSub || (a.Info != nil && a.Info.IsSub),
	}
}

type remotePrivilege struct {
	ID int64 `json:"id"`
	Status int `json:"st"`
}

type remoteSong struct {
	ID int64 `json:"id"`
	Name string `json:"name"`
	Artists []remoteArtist `json:"ar"`
	OldArtists []remoteArtist `json:"artists"`
	Album *remoteAlbum `json:"al"`
	OldAlbum *remoteAlbum `json:"album"`
	Duration int64 `json:"dt"`
	OldDuration int64 `json:"duration"`
	Privilege *remotePrivilege `json:"privilege"`
	Status int `json:"status"`
}

func (song remoteSong) track() music.Track {
	id := formatID(song.ID)
	artists := song.Artists
	if len(artists) == 0 {
		artists = song.OldArtists
	}
	var album music.Album
	if song.Album != nil {
		album = song.Album.album()
	} else if song.OldAlbum != nil {
		album = song.OldAlbum.album()
	}
	// Its source, which it plays through: the player asks the app for the
	// source of that ID, and a track without it has none.
	tr := music.Track{
		ID: id, Source: ID, Name: firstText(song.Name, "未知歌曲"), Album: album,
		Artists: convertSlice(artists, remoteArtist.artist), Duration: song.Duration,
		Unavailable: song.Status < 0 || (song.Privilege != nil && song.Privilege.Status < 0),
	}
	if id != "" {
		tr.URL = site + "/song?id=" + id
	}
	if tr.Duration == 0 {
		tr.Duration = song.OldDuration
	}
	return tr
}

func indexPrivileges(privileges []remotePrivilege) map[int64]*remotePrivilege {
	if len(privileges) == 0 {
		return nil
	}
	privilege := make(map[int64]*remotePrivilege, len(privileges))
	for i := range privileges {
		p := &privileges[i]
		privilege[p.ID] = p
	}
	return privilege
}

func convertTracks(songs []remoteSong, privileges []remotePrivilege) []music.Track {
	privilege := indexPrivileges(privileges)
	tracks := make([]music.Track, len(songs))
	for i, song := range songs {
		if p := privilege[song.ID]; p != nil {
			song.Privilege = p
		}
		tracks[i] = song.track()
	}
	return tracks
}

type songID struct {
	ID int64 `json:"id"`
}

type remotePlaylist struct {
	ID int64 `json:"id"`
	UserID int64 `json:"userId"`
	Name string `json:"name"`
	Cover string `json:"coverImgUrl"`
	Picture string `json:"picUrl"`
	Description string `json:"description"`
	Copywriter string `json:"copywriter"`
	Creator remoteUser `json:"creator"`
	Count int `json:"trackCount"`
	SongCount int `json:"songCount"`
	Subscribed bool `json:"subscribed"`
	SpecialType int `json:"specialType"`
	TrackIDs []songID `json:"trackIds"`
	Tracks []remoteSong `json:"tracks"`
}

func (p remotePlaylist) ownerID() int64 {
	if p.Creator.UserID != 0 {
		return p.Creator.UserID
	}
	return p.UserID
}

func (p remotePlaylist) playlist(viewer int64) music.Playlist {
	mine := viewer != 0 && p.ownerID() == viewer
	return music.Playlist{
		ID: formatID(p.ID), Name: p.Name, ImageURL: picture(firstText(p.Cover, p.Picture)),
		Description: firstText(p.Description, p.Copywriter), Owner: p.Creator.Name,
		TrackCount: max(p.Count, p.SongCount), Mine: mine, Saved: !mine && p.Subscribed,
		Fixed: mine && p.SpecialType == 5,
	}
}

type remoteShow struct {
	ID int64 `json:"id"`
	Name string `json:"name"`
	Picture string `json:"picUrl"`
	Description string `json:"desc"`
	DJ remoteUser `json:"dj"`
	Count int `json:"programCount"`
	Subscribed bool `json:"subscribed"`
	Subed bool `json:"subed"`
}

func (r remoteShow) show() music.Show {
	sh := music.Show{
		ID: formatID(r.ID), Name: r.Name, ImageURL: picture(r.Picture), Description: r.Description,
		Publisher: r.DJ.Name, EpisodeCount: r.Count, Subscribed: r.Subscribed || r.Subed,
	}
	if r.DJ.UserID > 0 {
		sh.PublisherPage = "user:" + formatID(r.DJ.UserID)
	}
	return sh
}

// Both IDs are required because episode comments and likes identify the
// program, while the audio URL identifies its main song.
func trackIDs(id string) (song, program int64, err error) {
	if suffix, found := strings.CutPrefix(id, "dj:"); found {
		p, a, _ := strings.Cut(suffix, ":")
		program, err = parseID(p)
		if err == nil && a != "" {
			song, err = parseID(a)
		}
		return
	}
	song, err = parseID(id)
	return
}

type remoteEpisode struct {
	ID int64 `json:"id"`
	Name string `json:"name"`
	Liked *bool `json:"liked"`
	Description string `json:"description"`
	Cover string `json:"coverUrl"`
	Published int64 `json:"createTime"`
	Duration int64 `json:"duration"`
	Song remoteSong `json:"mainSong"`
	DJ remoteUser `json:"dj"`
}

func (p remoteEpisode) episode(show music.Show) music.Track {
	tr := p.Song.track()
	tr.ID = "dj:" + formatID(p.ID) + ":" + formatID(p.Song.ID)
	tr.URL = site + "/program?id=" + formatID(p.ID)
	tr.Name = firstText(p.Name, tr.Name)
	tr.Album = music.Album{Name: show.Name, ImageURL: firstText(picture(p.Cover), show.ImageURL)}
	if publisher := firstText(p.DJ.Name, show.Publisher); publisher != "" {
		tr.Artists = []music.Artist{{Name: publisher}}
	}
	if p.Duration > 0 {
		tr.Duration = p.Duration
	}
	tr.ShowID, tr.Description, tr.Published = show.ID, p.Description, date(p.Published)
	tr.Unavailable = tr.Unavailable || p.Song.ID == 0
	return tr
}

type remoteComment struct {
	ID int64 `json:"commentId"`
	User remoteUser `json:"user"`
	Content string `json:"content"`
	Time int64 `json:"time"`
	Likes int `json:"likedCount"`
	Liked bool `json:"liked"`
}

func (c remoteComment) comment() music.Comment {
	return music.Comment{
		ID: formatID(c.ID), Author: firstText(c.User.Name, "网易云用户"), AvatarURL: picture(c.User.Avatar),
		Text: c.Content, Time: c.Time, Likes: c.Likes, Liked: c.Liked,
	}
}
