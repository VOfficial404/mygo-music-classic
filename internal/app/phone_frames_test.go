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

// With MM_FRAMES set, saves a playlist, the listener's tab, the settings and
// Now Playing on a phone.
func TestPhoneFrames(t *testing.T) {
	dir := framesDir(t)
	was := onPhone
	onPhone = true
	defer func() { onPhone = was }()
	a := phoneApp()
	tt := ui.NewTester(a.view, phoneW, phoneH)
	settled(tt)
	saveFrame(t, dir, "phone-playlist.png", tt.Image())

	a.library.home = &music.Browse{Sections: []music.Section{
		{Title: "Daily Picks", Tracks: a.playlists["10"].Tracks},
		{Title: "Playlists for You", Playlists: a.mine},
		{Title: "New Songs", Tracks: a.playlists["10"].Tracks[:4]},
	}}
	a.openTab(tabHome)
	settled(tt)
	saveFrame(t, dir, "phone-home.png", tt.Image())

	if err := tt.Click(T("Mine")); err != nil {
		t.Fatal(err)
	}
	settled(tt)
	saveFrame(t, dir, "phone-mine.png", tt.Image())

	a.open(route{page: pageSettings})
	settled(tt)
	saveFrame(t, dir, "phone-settings.png", tt.Image())

	a.library.stations = []music.Station{{ID: "fm", Name: "Personal FM"}, {ID: "heart", Name: "Heartbeat"}}
	a.library.stationsOnce = true
	a.pl.AttachRadio(radioName("fake", &a.library.stations[0]), func(func([]music.Track)) {})
	a.open(route{page: pageStations})
	settled(tt)
	saveFrame(t, dir, "phone-fm.png", tt.Image())

	a.nowPlaying, a.npPane = true, paneLyrics
	tt.SetDark(true)
	tt.Advance(2 * time.Second)
	tt.Frame()
	saveFrame(t, dir, "phone-now-playing.png", tt.Image())
}

// A tab opens its first page and forgets the pages opened before it, so the
// system's back leaves the app from there rather than going through them; a
// page opened from a tab stays under it.
func TestPhoneTabs(t *testing.T) {
	a := phoneApp()
	tt := ui.NewTester(a.view, phoneW, phoneH)
	settled(tt)
	a.open(route{page: pageAlbum, id: "1"})
	if err := tt.Click(T("Mine")); err != nil {
		t.Fatal(err)
	}
	if a.route.page != pageMine || len(a.history) != 0 {
		t.Fatalf("on %v with %d pages behind", a.route.page, len(a.history))
	}
	a.open(route{page: pageLiked})
	tt.Frame()
	if a.tabOf(a.route.page) != tabMine {
		t.Errorf("the liked songs are of tab %d", a.tabOf(a.route.page))
	}
	if err := tt.Click(T("Search")); err != nil {
		t.Fatal(err)
	}
	if a.route.page != pageSearchStart {
		t.Errorf("the search tab opened %v", a.route.page)
	}
}
