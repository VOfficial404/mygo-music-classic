package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-music/internal/lyrics"
	"mygo-music/internal/music"
)

// phoneW and phoneH are the size of a phone held upright, in DIPs.
const phoneW, phoneH = 390, 844

// phoneApp is an app with a playlist open and a song playing, as on a phone.
func phoneApp() *app {
	a := newTestApp()
	a.account = &music.Account{ID: "1", Name: "Alex"}
	tracks := framesTracks(a)
	a.mine = []music.Playlist{
		{ID: "10", Name: "Evening Walks", ImageURL: "cover:0", Mine: true},
		{ID: "11", Name: "Focus", ImageURL: "cover:1", Mine: true},
	}
	a.playlists["10"] = &music.Playlist{ID: "10", Name: "Evening Walks", Owner: "Alex", Description: "For the long way home.", ImageURL: "cover:0", Mine: true, Tracks: tracks}
	a.route = route{page: pagePlaylist, src: "fake", id: "10"}
	a.pl.Cue(tracks)
	a.pl.SetLyrics(lyrics.Build(
		"[00:04.00]The sky goes green at the edges\n[00:09.00]And everyone stops to look\n[00:14.00]Nobody says a word\n[00:19.00]Nobody needs to",
		"",
	))
	a.pl.Hold(10 * time.Second)
	return a
}

// With MM_FRAMES set, saves a playlist, the library drawer and Now Playing on
// a phone.
func TestPhoneFrames(t *testing.T) {
	dir := framesDir(t)
	a := phoneApp()
	tt := ui.NewTester(a.view, phoneW, phoneH)
	settled(tt)
	saveFrame(t, dir, "phone-playlist.png", tt.Image())

	if err := tt.Click("Library"); err != nil {
		t.Fatal(err)
	}
	settled(tt)
	settled(tt)
	saveFrame(t, dir, "phone-drawer.png", tt.Image())
	a.drawerOpen = false
	settled(tt)

	a.nowPlaying, a.npPane = true, paneLyrics
	tt.SetDark(true)
	tt.Advance(2 * time.Second)
	tt.Frame()
	saveFrame(t, dir, "phone-now-playing.png", tt.Image())

	a.npPane = paneQueue
	tt.Advance(time.Second)
	tt.Frame()
	saveFrame(t, dir, "phone-queue.png", tt.Image())
}
