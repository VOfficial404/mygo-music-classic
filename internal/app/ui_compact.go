package app

import (
	"runtime"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/mobile"

	"mygo-music/internal/music"
)

// compactWidth is the width below which the window takes the layout of
// phones: the library in a drawer, a slim player floating over the page, and
// Now Playing one pane at a time.
const compactWidth = 720

// isCompact reports whether the window is as narrow as a phone.
func isCompact(c *ui.Context) bool {
	w, _ := c.Size()
	return w < compactWidth
}

// onPhone reports whether the app runs on a phone or a tablet, which has
// hardware buttons for the volume and no pointer to hover.
var onPhone = runtime.GOOS == "android" || runtime.GOOS == "ios"

// compactBarHeight is the height of the player floating at the foot of the
// page in the compact layout, and compactBarGap the room around it.
const (
	compactBarHeight = 60
	compactBarGap    = 8
)

// drawer is the sidebar, sliding in from the side in the compact layout.
func (a *app) drawer(c *ui.Context) {
	if !isCompact(c) {
		a.drawerOpen = false
		return
	}
	w, _ := c.Size()
	p := a.pal
	mobile.Drawer(c, &a.drawerOpen, mobile.DrawerOptions{Width: min(300, w-56)}, func() {
		ui.Column(c.Key("drawer")).Fill().Background(p.sidebar).Children(func() { a.sidebar(c) })
	})
}

// menuButton opens the drawer, at the start of the strip over a page.
func (a *app) menuButton(c *ui.Context) {
	if iconButton(c.Key("menu"), iconMenu, 20, a.pal.text, T("Library")).Clicked() {
		a.drawerOpen = true
	}
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
	a.tickTimes(c)
	card := ui.Row(c.Key("mini-player")).Absolute().Left(compactBarGap).Right(compactBarGap).Bottom(compactBarGap).
		Height(compactBarHeight).Padding(0, 6, 0, 8).Gap(4).Radius(14).AlignItems(ui.Center).
		Background(p.sidebar).Border(1, p.hairline).Shadow(0, 6, 20, -6, p.cardShade)
	if tr != nil && pl.Duration() > 0 {
		done := float32(pl.Position()) / float32(pl.Duration())
		accent := p.accent
		card.DrawOver(func(pt *ui.Painter, r ui.Rect) {
			pt.Fill(ui.Rect{X: r.X + 14, Y: r.Y + r.H - 3, W: (r.W - 28) * min(max(done, 0), 1), H: 2}, accent, 1)
		})
	}
	card.Children(func() {
		if tr == nil {
			ui.Box(c).Size(44, 44).Radius(8).Background(p.hover).Center().Children(func() {
				ui.Icon(c, iconNote).Size(18, 18).TextColor(p.faint)
			})
			ui.Text(c, T("Not Playing")).FontSize(13).TextColor(p.muted).Margin(0, 0, 0, 8)
			return
		}
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
