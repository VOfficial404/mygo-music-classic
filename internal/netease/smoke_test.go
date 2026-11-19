//go:build smoke

package netease

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"mygo-music/internal/music"
)

// TestSmoke exercises the public catalog, playback and start of QR login.
func TestSmoke(t *testing.T) {
	s := New(t.TempDir())
	var track music.Track
	t.Run("Search", func(t *testing.T) {
		r, err := s.Search("春日影", music.SearchTracks, 0)
		if err != nil || len(r.Tracks) == 0 {
			t.Fatalf("search: %+v, %v", r, err)
		}
		track = r.Tracks[0]
		t.Logf("%d tracks, first %s (%s), more=%t", len(r.Tracks), track.Name, track.ID, r.More)
	})
	var show music.Show
	t.Run("SearchOtherKinds", func(t *testing.T) {
		for _, query := range []struct { term string; kind music.SearchKind }{
			{"迷跡波", music.SearchAlbums}, {"MyGO", music.SearchArtists},
			{"流行", music.SearchPlaylists}, {"音乐", music.SearchShows},
		} {
			page, err := s.Search(query.term, query.kind, 0)
			if err != nil || page.Len() == 0 {
				t.Errorf("%s: %+v, %v", query.kind, page, err)
				continue
			}
			if query.kind == music.SearchShows {
				show = page.Shows[0]
			}
			t.Logf("%s: %d, more=%t", query.kind, page.Len(), page.More)
		}
	})
	t.Run("Suggest", func(t *testing.T) {
		suggestions, err := s.Suggest("春日影")
		if err != nil || len(suggestions) == 0 {
			t.Fatalf("suggest: %v, %v", suggestions, err)
		}
		t.Logf("%d suggestions", len(suggestions))
	})
	if show.ID != "" {
		t.Run("Show", func(t *testing.T) {
			page, err := s.Show(show.ID, 0)
			if err != nil || len(page.Episodes) == 0 || page.Episodes[0].ShowID != show.ID {
				t.Fatalf("show: %v", err)
			}
			t.Logf("%s, %d episodes", page.Show.Name, len(page.Episodes))
		})
	}
	if track.ID != "" {
		t.Run("Album", func(t *testing.T) {
			page, err := s.Album(track.Album.ID)
			if err != nil || len(page.Tracks) == 0 {
				t.Fatalf("album: %+v, %v", page, err)
			}
			t.Logf("%s, %d tracks", page.Album.Name, len(page.Tracks))
		})
		t.Run("Artist", func(t *testing.T) {
			if len(track.Artists) == 0 {
				t.Fatal("search result has no artist")
			}
			page, err := s.Artist(track.Artists[0].ID)
			if err != nil || len(page.Tracks) == 0 || len(page.Albums) == 0 {
				t.Fatalf("artist: %+v, %v", page, err)
			}
			t.Logf("%s, %d songs, %d albums", page.Artist.Name, len(page.Tracks), len(page.Albums))
		})
		t.Run("Lyrics", func(t *testing.T) {
			ly, err := s.Lyrics(track.ID)
			if err != nil || ly.LRC == "" {
				t.Fatalf("lyrics: %+v, %v", ly, err)
			}
			t.Logf("春日影: %d LRC bytes, %d translated bytes", len(ly.LRC), len(ly.Translation))
			wordTimed, err := s.Lyrics("1974443814")
			if err != nil || len(wordTimed.Lines) == 0 || len(wordTimed.Lines[0].Words) == 0 {
				t.Fatalf("word-timed lyrics: %v", err)
			}
			t.Logf("1974443814: %d word-timed lines", len(wordTimed.Lines))
		})
		t.Run("Comments", func(t *testing.T) {
			page, err := s.Comments(track.ID, 0)
			if err != nil || len(page.Comments) == 0 {
				t.Fatalf("comments: %+v, %v", page, err)
			}
			next, err := s.Comments(track.ID, len(page.Comments))
			if err != nil || len(next.Comments) == 0 {
				t.Fatalf("next page: %+v, %v", next, err)
			}
			seen := make(map[string]bool, len(page.Comments))
			for _, comment := range page.Comments {
				seen[comment.ID] = true
			}
			for _, comment := range next.Comments {
				if seen[comment.ID] {
					t.Fatalf("repeat comment %s", comment.ID)
				}
			}
			t.Logf("%d + %d comments, total %d", len(page.Comments), len(next.Comments), page.Total)
		})
	}
	t.Run("Playlist", func(t *testing.T) {
		p, err := s.Playlist("3778678")
		if err != nil || len(p.Tracks) == 0 || len(p.Tracks) != p.TrackCount {
			t.Fatalf("chart tracks missing: %v", err)
		}
		t.Logf("%s, all %d tracks", p.Name, p.TrackCount)
	})
	t.Run("Stream", func(t *testing.T) {
		// The first search hit and chart licensing can change. Use public
		// fixtures as well, skipping only an explicit lack of rights.
		for _, id := range []string{"447926050", "1974443814", "1901371647", track.ID} {
			if id == "" {
				continue
			}
			stream, err := s.Stream(id, "standard")
			if errors.Is(err, music.ErrUnavailable) {
				t.Logf("%s: %v", id, err)
				continue
			}
			if err != nil || stream.URL == "" || stream.Type == "" {
				t.Fatalf("stream %s: %v", id, err)
			}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, stream.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			for key, value := range stream.Headers {
				req.Header.Set(key, value)
			}
			req.Header.Set("Range", "bytes=0-1023")
			response, err := s.client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusPartialContent && response.StatusCode != http.StatusOK {
				t.Fatalf("audio URL returned HTTP %d", response.StatusCode)
			}
			if n, err := io.CopyN(io.Discard, response.Body, 1024); err != nil || n != 1024 {
				t.Fatalf("audio URL returned %d bytes: %v", n, err)
			}
			t.Logf("%s, %s, %d bps, %d bytes", id, stream.Type, stream.BitRate, stream.Size)
			return
		}
		t.Fatal("no free music fixture available")
	})
	t.Run("Home", func(t *testing.T) {
		page, err := s.Home()
		if err != nil {
			t.Fatal(err)
		}
		var tracks, playlists, links, shows int
		for _, section := range page.Sections {
			tracks += len(section.Tracks)
			playlists += len(section.Playlists)
			links += len(section.Links)
			shows += len(section.Shows)
		}
		if tracks == 0 || playlists == 0 || links == 0 || shows == 0 {
			t.Errorf("public home sections missing")
		}
		t.Logf("%d sections, %d tracks, %d playlists, %d links, %d shows", len(page.Sections), tracks, playlists, links, shows)
	})
	t.Run("Browse", func(t *testing.T) {
		for _, name := range []string{"toplists", "artists", "playlists", "playlists:华语", "newsongs"} {
			page, err := s.Browse(name)
			if err != nil || len(page.Sections) == 0 {
				t.Errorf("%s: %v", name, err)
				continue
			}
			var items int
			for _, section := range page.Sections {
				items += len(section.Links) + len(section.Tracks) + len(section.Playlists) + len(section.Artists)
			}
			if items == 0 {
				t.Errorf("%s returned no items", name)
			}
			t.Logf("%s: %d items", name, items)
		}
	})
	t.Run("QRKey", func(t *testing.T) {
		state := &session{}
		key, err := s.qrKey(context.Background(), state)
		if err != nil || key == "" {
			t.Fatalf("QR key: %v", err)
		}
		var status apiStatus
		err = s.requestOn(context.Background(), state, desktop, "/api/login/qrcode/client/login", fields{"key": key, "type": 3}, &status, 801)
		if err != nil || status.Code != 801 {
			t.Fatalf("QR poll: code %d, %v", status.Code, err)
		}
		t.Log("QR key issued and waiting for scan (801)")
	})
}
