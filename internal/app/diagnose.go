package app

import (
	"context"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/audio"

	"mygo-music/internal/netease"
)

// The app's log, kept in memory as well as written where the system keeps
// it: on a phone, out of reach of the listener, who can copy it from the
// settings to send with a report.

// logLines is how many lines of the log are kept.
const logLines = 200

type logRing struct {
	mu    sync.Mutex
	lines []string
	part  string
}

func (r *logRing) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	text := r.part + string(b)
	parts := strings.Split(text, "\n")
	r.part = parts[len(parts)-1]
	r.lines = append(r.lines, parts[:len(parts)-1]...)
	if over := len(r.lines) - logLines; over > 0 {
		r.lines = append(r.lines[:0], r.lines[over:]...)
	}
	return len(b), nil
}

// last returns the last n lines.
func (r *logRing) last(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lines[max(0, len(r.lines)-n):]...)
}

var appLog = &logRing{}

// keepLog has the log written to the ring too.
func keepLog() {
	log.SetOutput(io.MultiWriter(os.Stderr, appLog))
}

// diagState is the playback test, as it goes.
type diagState struct {
	running bool
	lines   []string
}

// testSongID is a song anyone may play, signed in or not.
const testSongID = "447926050"

// runDiagnosis checks, one after another, what playing a song takes: the
// service answering where the song is, its download, and the sound output.
func (a *app) runDiagnosis() {
	if a.diag.running {
		return
	}
	a.diag = diagState{running: true}
	say := func(line string) {
		log.Print("diagnosis: " + line)
		a.post(func() { a.diag.lines = append(a.diag.lines, line) })
	}
	lib := a.libOf(netease.ID)
	go func() {
		defer a.post(func() { a.diag.running = false })
		say(Tf("System: %s", mygo.App.Version()+" · "+platformName()))
		if lib == nil {
			say(T("NetEase Cloud Music is turned off"))
		} else if url := a.diagnoseStream(lib, say); url != "" {
			a.diagnoseDownload(url, say)
		}
		a.diagnoseOutput(say)
	}()
}

func (a *app) diagnoseStream(lib *library, say func(string)) string {
	start := time.Now()
	st, err := lib.src.Stream(testSongID, "standard")
	if err != nil {
		say(Tf("✗ Asking for a song: %s", err.Error()))
		return ""
	}
	say(Tf("✓ Asking for a song: %s, %d ms", st.Type, time.Since(start).Milliseconds()))
	return st.URL
}

func (a *app) diagnoseDownload(url string, say func(string)) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("Range", "bytes=0-262143")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		say(Tf("✗ Downloading: %s", err.Error()))
		return
	}
	defer res.Body.Close()
	n, err := io.Copy(io.Discard, res.Body)
	if err != nil {
		say(Tf("✗ Downloading: %s", err.Error()))
		return
	}
	say(Tf("✓ Downloading: %s, %d KB in %d ms", res.Status, n>>10, time.Since(start).Milliseconds()))
}

// diagnoseOutput plays a quiet tone for a moment and checks that it was heard.
func (a *app) diagnoseOutput(say func(string)) {
	var out *audio.Player
	mygo.RunOnMain(func() {
		out = audio.NewPlayer(&testTone{})
		out.SetVolume(0.15).Play()
	})
	time.Sleep(2 * time.Second)
	var heard time.Duration
	mygo.RunOnMain(func() {
		heard = out.Position()
		out.Close()
	})
	switch err := audio.Err(); {
	case err != nil:
		say(Tf("✗ Sound output: %s", err.Error()))
	case heard < 500*time.Millisecond:
		say(Tf("✗ Sound output: %d ms heard of 2 s", heard.Milliseconds()))
	default:
		say(Tf("✓ Sound output: %d ms heard of 2 s", heard.Milliseconds()))
	}
}

// testTone is a second and a half of a soft A.
type testTone struct{ pos int64 }

const toneRate = 48000

func (t *testTone) SampleRate() int { return toneRate }
func (t *testTone) Channels() int   { return 1 }
func (t *testTone) Len() int64      { return toneRate * 3 / 2 }
func (t *testTone) SetPosition(f int64) error {
	t.pos = f
	return nil
}

func (t *testTone) Read(buf []float32) (int, error) {
	n := 0
	for n < len(buf) && t.pos < t.Len() {
		buf[n] = 0.2 * float32(math.Sin(2*math.Pi*440*float64(t.pos)/toneRate))
		t.pos++
		n++
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

// platformName is the system and its architecture, as a report wants it.
func platformName() string { return runtime.GOOS + "/" + runtime.GOARCH }
