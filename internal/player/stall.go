package player

import (
	"log"
	"time"

	"github.com/egoist/mygo/audio"

	"mygo-music/internal/i18n"
)

// A track can play without a sound being heard and without anything failing:
// the system's output takes no samples, so what's heard stays where it is.
// Nothing errs, and the listener would see the time stuck at 0:00 with no
// word of why. The player checks, every stallEvery while it plays, that what's
// heard moves, and says so once it hasn't for stallLimit.
const (
	stallEvery = 2 * time.Second
	// stallRetry is when the output is opened again, at the other latency:
	// some phones' output takes nothing at one buffer size and plays at
	// another.
	stallRetry = 6 * time.Second
	stallLimit = 10 * time.Second
)

// stallState is what the check last saw.
type stallState struct {
	on      bool // a check is on its way
	at      time.Duration
	still   time.Duration // how long what's heard hasn't moved
	retried bool          // the output was opened again, once for the app
	said    bool          // err says it doesn't move
}

// watchStall starts checking, if it isn't, while something plays.
func (p *Player) watchStall() {
	if p.stall.on || !p.playing || p.out == nil {
		return
	}
	p.stall.on = true
	p.stall.at, p.stall.still = -1, 0
	p.nextStallCheck()
}

func (p *Player) nextStallCheck() {
	p.after(stallEvery, func() {
		run := p.post
		if p.Quiet != nil {
			run = p.Quiet
		}
		run(p.checkStall)
	})
}

// checkStall runs on the main thread.
func (p *Player) checkStall() {
	out := p.out
	if !p.playing || out == nil {
		p.stall.on = false
		return
	}
	defer p.nextStallCheck()
	pos := out.Position()
	if p.Loading() || p.seeking || pos != p.stall.at {
		p.stall.at, p.stall.still = pos, 0
		if p.stall.said {
			p.stall.said, p.err = false, ""
			p.post(func() {})
		}
		return
	}
	p.stall.still += stallEvery
	switch {
	case !out.Playing():
		// Asked to play and paused all the same: play it.
		log.Printf("player: %q is paused while it should play; playing it", p.track.Name)
		out.Play()
		p.fade.fade(1, fadeIn, nil)
	case p.stall.still >= stallRetry && !p.stall.retried:
		p.stall.retried = true
		log.Printf("player: nothing heard of %q for %v at %v (output error: %v); opening the output again", p.track.Name, p.stall.still, pos, audio.Err())
		audio.SetLatency(audio.LatencyInteractive)
	case p.stall.still >= stallLimit && !p.stall.said:
		p.stall.said = true
		p.err = stallText()
		log.Printf("player: nothing heard of %q for %v: %s", p.track.Name, p.stall.still, p.err)
		p.post(func() {})
	}
}

// stallText says why nothing is heard, as far as the player can tell.
func stallText() string {
	if err := audio.Err(); err != nil {
		return i18n.Tf("Couldn't open the sound output: %s", err.Error())
	}
	return i18n.T("The system isn't taking any sound from the app. Check the volume and Bluetooth, or restart the app.")
}
