package app

import (
	"runtime"

	"github.com/egoist/mygo/ui"

	"mygo-music/internal/music"
)

// compactWidth is the width below which the window takes the layout of
// phones: tabs along the foot, a slim player over them, and Now Playing one
// pane at a time.
const compactWidth = 720

// isCompact reports whether the window is as narrow as a phone.
func isCompact(c *ui.Context) bool {
	w, _ := c.Size()
	return w < compactWidth
}

// onPhone reports whether the app runs on a phone or a tablet, which has
// hardware buttons for the volume and no pointer to hover.
var onPhone = runtime.GOOS == "android" || runtime.GOOS == "ios"

// compactBarHeight is the height of the player floating over the tabs in the
// compact layout, compactBarGap the room around it, and tabBarHeight the
// height of the tabs.
const (
	compactBarHeight = 60
	compactBarGap    = 8
	tabBarHeight     = 56
)

// The tabs of the compact layout.
const (
	tabHome = iota
	tabSearch
	tabMine
)

// compactBottom is the room the tabs, and the player over them while a song
// is loaded, take at the foot of a page.
func (a *app) compactBottom() float32 {
	if a.pl != nil && a.pl.Current() != nil {
		return tabBarHeight + compactBarHeight + 2*compactBarGap
	}
	return tabBarHeight
}

// tabOf is the tab a page belongs to: what it was opened from, for a page
// opened from another.
func (a *app) tabOf(page pageKind) int {
	switch page {
	case pageHome:
		return tabHome
	case pageSearch, pageSearchStart:
		return tabSearch
	case pageMine, pageSettings:
		return tabMine
	}
	return a.phoneTab
}

// openTab goes to the first page of a tab, leaving the pages opened before,
// as tabs of a phone's apps do: the back button leaves the app from there.
func (a *app) openTab(tab int) {
	a.nowPlaying = false
	a.history, a.forward = nil, nil
	a.phoneTab = tab
	r := route{page: pageHome}
	switch tab {
	case tabSearch:
		r = route{page: pageSearchStart}
		if a.searched.query != "" && a.searched.src == a.id {
			r = a.searched
		} else {
			a.focusSearch = true
		}
	case tabMine:
		r = route{page: pageMine}
	}
	a.show(r)
}

// tabBar runs along the foot of the window in the compact layout.
func (a *app) tabBar(c *ui.Context) {
	p := a.pal
	at := a.tabOf(a.route.page)
	a.phoneTab = at
	ui.Row(c.Key("tab-bar")).Absolute().Left(0).Right(0).Bottom(0).Height(tabBarHeight).
		Background(p.sidebar).AlignItems(ui.Stretch).Children(func() {
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Height(0.5).Background(p.hairline)
		for _, t := range []struct {
			tab   int
			icon  *ui.SVG
			label string
		}{{tabHome, iconHome, T("Home")}, {tabSearch, iconSearch, T("Search")}, {tabMine, iconUser, T("Mine")}} {
			color := p.muted
			if t.tab == at {
				color = p.accent
			}
			b := ui.ButtonBase(c.Key(t.label)).Grow(1).Basis(0).Gap(3).Center().FocusRing(false).Label(t.label)
			b.Children(func() {
				ui.Icon(c, t.icon).Size(22, 22).TextColor(color)
				ui.Text(c, t.label).FontSize(10.5).FontWeight(600).TextColor(color)
			})
			if b.Clicked() {
				a.openTab(t.tab)
			}
		}
	})
}

// minePage is the listener's own tab in the compact layout: who's signed in,
// the source's pages, their playlists, and the settings.
func (a *app) minePage(c *ui.Context) {
	pad := pagePadding(c)
	top, bottom := a.insets(c)
	ui.Scroll(c.Key(a.route)).TrackScroll(&a.pageScroll).Grow(1).Padding(top+pageGap, pad-12, bottom+24, pad-12).
		ScrollbarInsets(top, 0, bottom, 0).Gap(4).Children(func() {
		a.startMark(c)
		defer a.endMark(c)
		a.mineHeader(c)
		a.sourceSwitcher(c)
		for _, n := range a.sidebarItems() {
			if n.route.page != pageHome {
				a.navItem(c, n.icon, n.label, n.route)
			}
		}
		if a.can("playlists") {
			a.myPlaylists(c, T("My Playlists"), a.mine, true, &a.mineCollapsed)
			a.myPlaylists(c, T("Saved Playlists"), a.saved, false, &a.savedCollapsed)
		}
		ui.Box(c).Height(16)
		a.navItem(c, iconSettings, T("Settings"), route{page: pageSettings})
	})
}

// mineHeader is who's signed in, large, with what they can do about it.
func (a *app) mineHeader(c *ui.Context) {
	p := a.pal
	lib := a.library
	if lib.account == nil && lib.accountUnknown() {
		a.accountRow(c)
		return
	}
	ui.Row(c.Key("me")).Gap(14).AlignItems(ui.Center).Padding(4, 10, 18, 10).Children(func() {
		acc := lib.account
		if acc == nil {
			ui.Box(c).Size(56, 56).Radius(28).Background(p.press).Center().Children(func() {
				ui.Icon(c, iconUser).Size(26, 26).TextColor(p.muted)
			})
			if lib.can("signIn") && pillButton(c.Key("sign-in"), nil, Tf("Sign in to %s", lib.name()), p.accent, p.onAccent).Clicked() {
				a.promptSignIn(lib)
			}
			return
		}
		a.avatar(c, acc.ImageURL, 56, p.press)
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(8).Children(func() {
			ui.Text(c, acc.Name).FontSize(19).FontWeight(700).SingleLine()
			ui.Row(c).Gap(8).Children(func() {
				if acc.Page != "" && pillButton(c.Key("my-page"), nil, T("My Page"), p.hover.Over(p.bg), p.text).Clicked() {
					a.openMyPage(lib)
				}
				if lib.can("signOut") && pillButton(c.Key("sign-out"), nil, T("Sign Out"), p.hover.Over(p.bg), p.text).Clicked() {
					a.signOut(lib)
				}
			})
		})
	})
}

// searchStartPage is the search tab before anything is searched for: the
// field, and the searches before, a tap from searching again.
func (a *app) searchStartPage(c *ui.Context) {
	p := a.pal
	pad := pagePadding(c)
	top, bottom := a.insets(c)
	ui.Scroll(c.Key(a.route)).TrackScroll(&a.pageScroll).Grow(1).Padding(top+pageGap, pad, bottom+24, pad).Children(func() {
		a.startMark(c)
		defer a.endMark(c)
		a.searchField(c)
		if a.focusSearch {
			a.focusSearch = false
			a.searchBox.Focus(c)
		}
		if len(a.settings.SearchHistory) == 0 {
			return
		}
		ui.Row(c).AlignItems(ui.Center).Margin(10, 0, 12, 0).Children(func() {
			ui.Text(c, T("Recent Searches")).FontSize(15).FontWeight(700).Grow(1)
			if iconButton(c.Key("clear-history"), iconTrash, 16, p.muted, T("Clear")).Clicked() {
				a.clearSearchHistory()
			}
		})
		ui.Row(c).Wrap().Gap(8).Children(func() {
			for _, q := range a.settings.SearchHistory {
				if pillButton(c.Key(q), nil, q, p.hover.Over(p.bg), p.text).Clicked() {
					a.search(q)
				}
			}
		})
	})
}

// heroRow lays out the head of a page: its picture beside its title, or
// above it on a phone, where they don't fit side by side.
func heroRow(c *ui.Context) ui.Element {
	if isCompact(c) {
		return ui.Column(c).Gap(18).AlignItems(ui.Stretch)
	}
	return ui.Row(c).Gap(28).AlignItems(ui.End)
}

// compactPlayerBar is what plays, floating over the foot of the page as a
// card: its cover and title open Now Playing, beside play and next, over a
// line of its progress.
func (a *app) compactPlayerBar(c *ui.Context) {
	p := a.pal
	pl := a.pl
	tr := pl.Current()
	if tr == nil {
		return
	}
	a.tickTimes(c)
	card := ui.Row(c.Key("mini-player")).Absolute().Left(compactBarGap).Right(compactBarGap).Bottom(tabBarHeight+compactBarGap).
		Height(compactBarHeight).Padding(0, 6, 0, 8).Gap(4).Radius(14).AlignItems(ui.Center).
		Background(p.sidebar).Border(1, p.hairline).Shadow(0, 6, 20, -6, p.cardShade)
	if pl.Duration() > 0 {
		done := float32(pl.Position()) / float32(pl.Duration())
		accent := p.accent
		card.DrawOver(func(pt *ui.Painter, r ui.Rect) {
			pt.Fill(ui.Rect{X: r.X + 14, Y: r.Y + r.H - 3, W: (r.W - 28) * min(max(done, 0), 1), H: 2}, accent, 1)
		})
	}
	card.Children(func() {
		info := ui.ButtonBase(c.Key("info")).Grow(1).Shrink(1).MinWidth(0).Gap(10).AlignItems(ui.Center).Justify(ui.Start).
			FocusRing(false).Label(T("Open Now Playing"))
		info.Children(func() {
			mini := ui.Box(c.Key("mini-cover")).Size(44, 44).Shrink(0)
			if b := mini.LayoutBounds(); b.W > 0 {
				a.npFrom = b
			}
			if a.npT > 0 && !a.npStill {
				mini.Opacity(0)
			}
			mini.Children(func() { a.cover(c, tr.Album.ImageURL, 120, 44, npFromRadius) })
			ui.Column(c).Shrink(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, tr.Name).FontSize(14).FontWeight(600).SingleLine()
				sub, color := tr.ArtistNames(), p.muted
				switch {
				case pl.Loading():
					sub = T("Buffering…")
				case pl.Err() != "":
					sub, color = pl.Err(), p.accent
				}
				ui.Text(c, sub).FontSize(12).TextColor(color).SingleLine()
			})
		})
		if info.Clicked() {
			a.nowPlaying = true
		}
		a.likeButton(c.Key("like"), tr, 18, p.muted).Shrink(0)
		if playButton(c.Key("play"), pl.Playing(), 30, p.text).Shrink(0).Clicked() {
			pl.TogglePlay()
		}
		if iconButton(c.Key("next"), iconNext, 20, p.text, T("Next")).Shrink(0).Clicked() {
			pl.Next()
		}
	})
}

// npCompactBody is Now Playing on a phone: one pane at a time under its tabs,
// the cover first, over what plays and its controls.
func (a *app) npCompactBody(c *ui.Context, tr *music.Track, art *artwork, accent ui.Color, w, h float32) {
	pl := a.pl
	padX := float32(24)
	ui.Row(c).Height(topBar(c)).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
		if iconButton(c.Key("close"), iconDown, 24, white.Alpha(0.75), T("Close")).Clicked() {
			a.nowPlaying = false
		}
	})
	a.npRise(ui.ScrollHorizontal(c.Key("tabs")).Height(npTabsHeight+4).Padding(0, padX).Gap(4).AlignItems(ui.Center), 0).
		Children(func() { a.paneTabs(c) })
	body := ui.Column(c.Key("pane")).Grow(1).Basis(0).MinHeight(0).Padding(16, padX, 0, padX)
	if a.npPane == paneCover {
		body.Justify(ui.Center).AlignItems(ui.Center)
	}
	body.Children(func() {
		switch a.npPane {
		case paneCover:
			size := max(min(w-2*padX, h-420), 120)
			a.npMorph(c, ui.Box(c.Key("cover-morph")).Size(size, size)).Children(func() {
				a.npCover(c, tr, art, size)
			})
		case paneLyrics:
			a.lyricsPane(c, tr, h)
		case paneComments:
			a.commentsPane(c, tr)
		case paneShow:
			a.showPane(c, tr)
		default:
			a.queuePane(c)
		}
	})
	ui.Column(c).Padding(12, padX, 28, padX).Children(func() {
		a.npRise(ui.Row(c).Gap(8).AlignItems(ui.Center).Margin(0, 0, 14, 0), 1).Children(func() {
			ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(4).Children(func() {
				if r := pl.Radio(); r != "" {
					a.radioChip(c, r)
				}
				ui.Text(c, tr.Name).FontSize(20).FontWeight(700).LetterSpacing(-0.3).SingleLine()
				a.artistLinks(c, tr, white.Alpha(0.6), white, true).FontSize(14).SingleLine()
				// Why it doesn't play, where it can't be missed.
				if err := pl.Err(); err != "" {
					ui.Text(c, err).FontSize(13).FontWeight(600).TextColor(ui.Hex("#ff8a8a")).MaxLines(2)
				}
			})
			a.likeButton(c.Key("like"), tr, 22, white.Alpha(0.7))
			a.songMenu(c.Key("more"), tr, 22, white.Alpha(0.7)).Margin(0, -8, 0, 0)
		})
		a.npRise(a.scrubber(c.Key("np-scrub"), &a.npScrub, white.Alpha(0.18), white.Alpha(0.9), 16, accent.Mix(white, 0.35)), 2)
		a.npRise(ui.Row(c).Height(16).AlignItems(ui.Center), 2).Children(func() {
			ui.Text(c, clock(pl.Position())).FontSize(11).FontFeatures("tnum").TextColor(white.Alpha(0.5))
			ui.Spacer(c)
			qualityBadge(c, pl.Stream())
			ui.Spacer(c)
			status := "-" + clock(pl.Duration()-pl.Position())
			if pl.Loading() {
				status = T("Buffering…")
			}
			ui.Text(c, status).FontSize(11).FontFeatures("tnum").TextColor(white.Alpha(0.5))
		})
		a.npRise(ui.Box(c).Margin(12, 0, 0, 0), 3).Children(func() {
			a.transport(c, white, white.Alpha(0.45), accent, 30)
		})
		if !onPhone {
			a.npRise(ui.Row(c).Justify(ui.Center).Margin(14, 0, 0, 0), 4).Children(func() {
				a.volumeSlider(c, white.Alpha(0.7), white.Alpha(0.18), 180)
			})
		}
	})
}
