package netease

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"mygo-music/internal/music"
)

type lyricText struct {
	Lyric string `json:"lyric"`
}

type lyricResponse struct {
	apiStatus
	LRC lyricText `json:"lrc"`
	Translation lyricText `json:"tlyric"`
	YRC lyricText `json:"yrc"`
	YRLTranslation lyricText `json:"ytlrc"`
	YRTranslation lyricText `json:"ytlyric"`
}

func (s *Source) Lyrics(id string) (*music.Lyrics, error) {
	n, program, err := trackIDs(id)
	if err != nil {
		return nil, err
	}
	if program != 0 {
		return &music.Lyrics{}, nil
	}
	state, _ := s.session(false)
	var modern lyricResponse
	params := fields{"id": n, "cp": false, "tv": 0, "lv": 0, "rv": 0, "kv": 0, "yv": 0, "ytv": 0, "yrv": 0}
	modernErr := s.requestOn(context.Background(), state, desktopRead, "/api/song/lyric/v1", params, &modern)
	if modernErr == nil {
		ly := modern.lyrics()
		if len(ly.Lines) > 0 || ly.LRC != "" {
			return ly, nil
		}
	} else if errors.Is(modernErr, music.ErrSignIn) {
		return nil, modernErr
	}
	var legacy lyricResponse
	params = fields{"id": n, "lv": 1, "tv": -1}
	if err := s.requestOn(context.Background(), state, web, "/api/song/lyric", params, &legacy); err != nil {
		return nil, errors.Join(modernErr, err)
	}
	return legacy.lyrics(), nil
}

func (r lyricResponse) lyrics() *music.Lyrics {
	result := &music.Lyrics{LRC: r.LRC.Lyric, Translation: r.Translation.Lyric, Lines: parseYRC(r.YRC.Lyric)}
	if len(result.Lines) > 0 {
		translations := parseLRC(r.Translation.Lyric)
		if len(translations) == 0 {
			translations = parseLRC(firstText(r.YRLTranslation.Lyric, r.YRTranslation.Lyric))
		}
		if len(translations) == 0 {
			translations = parseYRC(r.YRTranslation.Lyric)
		}
		alignTranslations(result.Lines, translations)
	}
	return result
}

var (
	yrcLine = regexp.MustCompile(`^\[(\d+),(\d+)\]`)
	yrcWord = regexp.MustCompile(`\((\d+),(\d+),-?\d+\)`)
	lrcStamp = regexp.MustCompile(`^\[(\d+):(\d{1,2})(?:[.:](\d+))?\]`)
)

func number(text string) int64 {
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func byStart(a, b music.LyricLine) int {
	switch {
	case a.Start < b.Start:
		return -1
	case a.Start > b.Start:
		return 1
	default:
		return 0
	}
}

func parseYRC(raw string) []music.LyricLine {
	var lines []music.LyricLine
	for row := range strings.SplitSeq(strings.TrimPrefix(raw, "\uFEFF"), "\n") {
		row = strings.TrimSuffix(row, "\r")
		m := yrcLine.FindStringSubmatchIndex(row)
		if m == nil {
			continue
		}
		start := number(row[m[2]:m[3]])
		line := music.LyricLine{Start: start, End: start + number(row[m[4]:m[5]])}
		content := row[m[1]:]
		matches := yrcWord.FindAllStringSubmatchIndex(content, -1)
		if len(matches) == 0 {
			continue
		}
		var text strings.Builder
		line.Words = make([]music.LyricWord, 0, len(matches))
		for i, word := range matches {
			endOfWord := len(content)
			if i+1 < len(matches) {
				endOfWord = matches[i+1][0]
			}
			wordText := content[word[1]:endOfWord]
			if wordText == "" {
				continue
			}
			at := number(content[word[2]:word[3]])
			end := at + number(content[word[4]:word[5]])
			line.Words = append(line.Words, music.LyricWord{Start: at, End: end, Text: wordText})
			text.WriteString(wordText)
		}
		if len(line.Words) > 0 {
			line.Text = text.String()
			lines = append(lines, line)
		}
	}
	slices.SortStableFunc(lines, byStart)
	return lines
}

func parseLRC(raw string) []music.LyricLine {
	var lines []music.LyricLine
	for row := range strings.SplitSeq(strings.TrimPrefix(raw, "\uFEFF"), "\n") {
		var times []int64
		for m := lrcStamp.FindStringSubmatchIndex(row); m != nil; m = lrcStamp.FindStringSubmatchIndex(row) {
			at := 60000*number(row[m[2]:m[3]]) + 1000*number(row[m[4]:m[5]])
			if m[6] >= 0 {
				frac := row[m[6]:min(m[7], m[6]+3)]
				n := number(frac)
				for range 3-len(frac) {
					n *= 10
				}
				at += n
			}
			times = append(times, at)
			row = row[m[1]:]
		}
		if text := strings.TrimSpace(row); text != "" {
			for _, at := range times {
				lines = append(lines, music.LyricLine{Start: at, Text: text})
			}
		}
	}
	slices.SortStableFunc(lines, byStart)
	return lines
}

func alignTranslations(lines, translated []music.LyricLine) {
	for i := range lines {
		at := lines[i].Start
		j, exact := slices.BinarySearchFunc(translated, at, func(line music.LyricLine, at int64) int {
			return byStart(line, music.LyricLine{Start: at})
		})
		if !exact && j > 0 && (j == len(translated) || at-translated[j-1].Start <= translated[j].Start-at) {
			j--
		}
		if j < len(translated) {
			if gap := translated[j].Start-at; gap >= -500 && gap <= 500 {
				lines[i].Translation = translated[j].Text
			}
		}
	}
}
