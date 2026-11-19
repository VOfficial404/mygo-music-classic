package netease

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"mygo-music/internal/music"
)

const cryptoJSON = `{"id":347230,"lv":0}`

// TestWeapiVector checks both CBC passes and the unpadded RSA operation against OpenSSL output.
func TestWeapiVector(t *testing.T) {
	secret := [16]byte{'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j', 'k', 'l', 'm', 'n', 'o', 'p'}
	got := weapi([]byte(cryptoJSON), secret)
	const params = "WzIdGTRcu1gPrnFm6ch3UH5WqIoVBABR34abTj1K/OgIwsDu0BlJLWGLI3FGdo8+"
	const encryptedSecret = "d15a1683c992095d0c234c19966605c5c5964911268bbeda8cb8d08d834913e59d53b32358903a121b5fca784c1f5ae44951fd02524df58ecc98e52cc7cf8689b42c2e93ddf05b0592512d87f5960467e2f086c018849d76014d323500e30f13ef4cafbb0cf5a66731a3f1776c75ca35d0062dac70a3e33245afabcf47938487"
	if got.Get("params") != params || got.Get("encSecKey") != encryptedSecret {
		t.Fatalf("weapi vector mismatch: %v", got)
	}
}

// TestEapiVector checks the wire digest, separators, padding and encoding against OpenSSL output.
func TestEapiVector(t *testing.T) {
	// The independent MD5 digest is 299d6dac2666ac7b343a563dbfdce4e4.
	const want = "04AE33D34A93FE3EC22DA8FA305D290AB337D0FE5F36D211DE0D338CC6AA89D09048AA189F3006406AE552A0207A0F8290F3AD169187D407BA0393D878DA7A012826CAA8B9E1C146D5B1BC7DA95905D0160198A02915044D41213199EF96C1126AA3B102FBE7296AB0DB9EA5C46AD12B"
	if got := eapi("/api/song/lyric/v1", []byte(cryptoJSON)); got != want {
		t.Fatalf("eapi vector mismatch: %s", got)
	}
}

// TestSongConversion checks old and new catalog shapes, exact IDs, artwork and
// privileges, and that a track names NetEase as its source: without it, the
// player found no source to play it through, and said it was turned off.
func TestSongConversion(t *testing.T) {
	const fixture = `{
		"songs":[
			{"id":9007199254740993,"name":"春日影","dt":256120,"ar":[{"id":7,"name":"MyGO!!!!!"}],"al":{"id":8,"name":"迷跡波","picUrl":"http://p1.music.126.net/cover.png?param=20y20&foo=x"}},
			{"id":60,"name":"另一首歌","duration":42,"artists":[{"id":9,"name":"乐队"}],"album":{"id":10,"name":"现场"},"privilege":{"st":-1}}
		],"privileges":[{"id":9007199254740993,"st":-200},{"id":60,"st":0}]
	}`
	var reply struct {
		Songs []remoteSong `json:"songs"`
		Privileges []remotePrivilege `json:"privileges"`
	}
	if err := json.Unmarshal([]byte(fixture), &reply); err != nil {
		t.Fatal(err)
	}
	tracks := convertTracks(reply.Songs, reply.Privileges)
	first, second := tracks[0], tracks[1]
	if first.ID != "9007199254740993" || first.URL != site+"/song?id="+first.ID || first.Duration != 256120 || !first.Unavailable || first.ArtistNames() != "MyGO!!!!!" || first.Source != ID {
		t.Fatalf("first track: %+v", first)
	}
	if want := "https://p1.music.126.net/cover.png?param={size}y{size}"; first.Album.ImageURL != want {
		t.Errorf("image = %q, want %q", first.Album.ImageURL, want)
	}
	if second.Duration != 42 || second.Album.ID != "10" || second.ArtistNames() != "乐队" || second.Unavailable || second.Source != ID {
		t.Fatalf("second track: %+v", second)
	}
}

// TestPlaylistAndEpisodeConversion checks ownership and the distinct program and audio IDs.
func TestPlaylistAndEpisodeConversion(t *testing.T) {
	const fixture = `[{"id":2,"userId":11,"specialType":5,"trackCount":600},{"id":3,"creator":{"nickname":"作者","userId":12},"subscribed":true,"songCount":30}]`
	var rows []remotePlaylist
	if err := json.Unmarshal([]byte(fixture), &rows); err != nil {
		t.Fatal(err)
	}
	mine, saved := rows[0].playlist(11), rows[1].playlist(11)
	if !mine.Mine || !mine.Fixed || mine.Saved || mine.TrackCount != 600 || saved.Mine || saved.Fixed || !saved.Saved || saved.Owner != "作者" || saved.TrackCount != 30 {
		t.Fatalf("playlist flags or metadata: mine=%+v saved=%+v", mine, saved)
	}
	var episode remoteEpisode
	if err := json.Unmarshal([]byte(`{"id":32,"name":"节目","createTime":1704067200000,"description":"简介","mainSong":{"id":44,"duration":4320}}`), &episode); err != nil {
		t.Fatal(err)
	}
	show := (remoteShow{ID: 1, Name: "播客", DJ: remoteUser{UserID: 20, Name: "主播"}}).show()
	track := episode.episode(show)
	audio, program, err := trackIDs(track.ID)
	if err != nil || audio != 44 || program != 32 || track.ShowID != "1" || track.Duration != 4320 || track.Published != "2024-01-01" || track.ArtistNames() != "主播" || track.Album.Name != "播客" || show.PublisherPage != "user:20" {
		t.Fatalf("episode: %+v, audio=%d program=%d err=%v", track, audio, program, err)
	}
}

// TestWordTiming checks absolute YRC timings and associates translations by their closest timestamp.
func TestWordTiming(t *testing.T) {
	r := lyricResponse{
		YRC: lyricText{`{"t":0,"c":[{"tx":"作词"}]}
[2700,720](2700,340,0)世界(3040,380,0)！
[1000,1300](1000,300,0)春(1300,200,0)日(1500,800,0) 影`},
		LRC: lyricText{"[00:01.000]春日 影"},
		Translation: lyricText{"[ar:歌手]\n[00:01.08]Spring sunlight\n[00:02.71]World!\n[00:10]Too late"},
	}
	lyrics := r.lyrics()
	if len(lyrics.Lines) != 2 {
		t.Fatalf("parsed %d timed lines", len(lyrics.Lines))
	}
	first, second := lyrics.Lines[0], lyrics.Lines[1]
	if first.Start != 1000 || first.End != 2300 || first.Text != "春日 影" || first.Translation != "Spring sunlight" || len(first.Words) != 3 {
		t.Fatalf("first timed line: %+v", first)
	}
	if word := first.Words[2]; word != (music.LyricWord{Start: 1500, End: 2300, Text: " 影"}) {
		t.Errorf("last word: %+v", word)
	}
	if second.Start != 2700 || second.End != 3420 || second.Translation != "World!" {
		t.Fatalf("second timed line: %+v", second)
	}
}

// TestLRCTiming checks repeated timestamps, fraction precision and the plain LRC fallback.
func TestLRCTiming(t *testing.T) {
	const lrc = "\uFEFF[ar:歌手]\r\n[00:06.1209][01:02.4]重复\r\n[00:01:04]开场\r\n[00:10] "
	lines := parseLRC(lrc)
	if len(lines) != 3 || lines[0].Start != 1040 || lines[1].Start != 6120 || lines[2].Start != 62400 || lines[2].Text != "重复" {
		t.Fatalf("LRC timings: %+v", lines)
	}
	const translation = "[00:01.04]Introduction"
	r := (lyricResponse{LRC: lyricText{lrc}, Translation: lyricText{translation}}).lyrics()
	if r.LRC != lrc || r.Translation != translation || len(r.Lines) != 0 {
		t.Fatalf("plain LRC fallback: %+v", r)
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// TestPlaylistBatches checks the entire list is returned in playlist order beyond 1000 tracks.
func TestPlaylistBatches(t *testing.T) {
	const count = 1103
	const missing = 1003
	s := New(t.TempDir())
	var batches int
	s.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		defer req.Body.Close()
		var payload any
		switch req.URL.Path {
		case "/weapi/v6/playlist/detail":
			ids := make([]songID, count)
			for i := range ids {
				ids[i].ID = int64(i + 1)
			}
			payload = fields{"code": 200, "playlist": fields{
				"id": 4321, "name": "歌单", "trackCount": count, "trackIds": ids,
				"tracks": []fields{{"id": 1}},
			}}
		case "/weapi/v3/song/detail":
			start := batches*500 + 1
			end := min(count, start+499)
			var songs []fields
			for id := end; id >= start; id-- {
				if id != missing {
					songs = append(songs, fields{"id": id, "name": "歌曲 " + strconv.Itoa(id)})
				}
			}
			payload = fields{"code": 200, "songs": songs}
			batches++
		default:
			return nil, fmt.Errorf("unexpected endpoint %q", req.URL.Path)
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Request: req}, nil
	})
	p, err := s.Playlist("4321")
	if err != nil {
		t.Fatal(err)
	}
	if batches != 3 || len(p.Tracks) != count || p.TrackCount != count {
		t.Fatalf("got %d/%d tracks across %d batches", len(p.Tracks), p.TrackCount, batches)
	}
	for i, track := range p.Tracks {
		id := i + 1
		if track.ID != strconv.Itoa(id) || track.Unavailable != (id == missing) {
			t.Fatalf("track %d: %+v", id, track)
		}
	}
}

// TestSessionCookies checks that a new source reloads cookies and that logout clears them on disk.
func TestSessionCookies(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if err := s.collectCookies(s.current, []*http.Cookie{
		{Name: "MUSIC_U", Value: "fixture"},
		{Name: "__csrf", Value: "csrf-fixture"},
		{Name: "NMTID", Value: "device-fixture"},
	}); err != nil {
		t.Fatal(err)
	}
	reloaded := New(dir)
	if _, err := reloaded.session(true); err != nil || reloaded.deviceID != s.deviceID {
		t.Fatalf("session did not survive startup: %v", err)
	}
	var called bool
	reloaded.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		defer req.Body.Close()
		if req.URL.Path != "/weapi/logout" {
			return nil, fmt.Errorf("unexpected endpoint %q", req.URL.Path)
		}
		for name, value := range map[string]string{"MUSIC_U": "fixture", "__csrf": "csrf-fixture", "NMTID": "device-fixture"} {
			cookie, err := req.Cookie(name)
			if err != nil || cookie.Value != value {
				return nil, fmt.Errorf("logout missing %s", name)
			}
		}
		called = true
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":200}`)), Request: req}, nil
	})
	if err := reloaded.SignOut(); err != nil || !called {
		t.Fatalf("logout request did not complete: %v", err)
	}
	if _, err := New(dir).session(true); !errors.Is(err, music.ErrSignIn) {
		t.Fatalf("logout left a saved session: %v", err)
	}
}
