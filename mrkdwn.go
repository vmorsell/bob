package main

import (
	"regexp"
	"strings"
)

// boldPlaceholder is a sentinel byte used to represent Slack bold markers (*)
// during transformation, so the italic step doesn't consume them.
const boldPlaceholder = "\x01"

// Compiled regexes for Markdown → Slack mrkdwn conversion.
var (
	reHeader     = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	reImage      = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)
	reLink       = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	reBoldItalic = regexp.MustCompile(`\*{3}(.+?)\*{3}`)
	reBold       = regexp.MustCompile(`\*{2}(.+?)\*{2}`)
	reItalic     = regexp.MustCompile(`\*(.+?)\*`)
	reStrike     = regexp.MustCompile(`~~(.+?)~~`)
)

// segment represents a chunk of text that is either code (to be preserved
// verbatim) or regular text (to be transformed).
type segment struct {
	text   string
	isCode bool
}

// splitCodeSegments splits s into alternating code and non-code segments.
// Fenced code blocks (```) are matched first, then inline code (`).
func splitCodeSegments(s string) []segment {
	var segments []segment

	// First pass: split on fenced code blocks.
	fenced := regexp.MustCompile("(?s)```[^\n]*\n.*?```")
	parts := splitByRegex(s, fenced)

	// Second pass: split non-code parts on inline code.
	inline := regexp.MustCompile("`[^`]+`")
	for _, p := range parts {
		if p.isCode {
			segments = append(segments, p)
			continue
		}
		segments = append(segments, splitByRegex(p.text, inline)...)
	}

	return segments
}

// splitByRegex splits text into segments, marking regex matches as code.
func splitByRegex(s string, re *regexp.Regexp) []segment {
	locs := re.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		if s != "" {
			return []segment{{text: s}}
		}
		return nil
	}

	var segments []segment
	prev := 0
	for _, loc := range locs {
		if loc[0] > prev {
			segments = append(segments, segment{text: s[prev:loc[0]]})
		}
		segments = append(segments, segment{text: s[loc[0]:loc[1]], isCode: true})
		prev = loc[1]
	}
	if prev < len(s) {
		segments = append(segments, segment{text: s[prev:]})
	}
	return segments
}

// transformMarkdown applies Markdown → Slack mrkdwn conversions to a non-code
// text segment. Uses a placeholder for Slack bold markers so the italic step
// doesn't consume them.
//
// Order:
//  1. Headers → bold placeholder
//  2. Images (before links — superset pattern)
//  3. Links
//  4. Bold-italic → bold placeholder + italic
//  5. Bold → bold placeholder
//  6. Italic → Slack italic (safe — all ** and placeholders already consumed)
//  7. Strikethrough
//  8. Replace placeholders with *
func transformMarkdown(s string) string {
	s = reHeader.ReplaceAllString(s, boldPlaceholder+"$1"+boldPlaceholder)
	s = reImage.ReplaceAllString(s, "<$2|$1>")
	s = reLink.ReplaceAllString(s, "<$2|$1>")
	s = reBoldItalic.ReplaceAllString(s, boldPlaceholder+"_${1}_"+boldPlaceholder)
	s = reBold.ReplaceAllString(s, boldPlaceholder+"${1}"+boldPlaceholder)
	s = reItalic.ReplaceAllString(s, "_${1}_")
	s = reStrike.ReplaceAllString(s, "~${1}~")
	s = strings.ReplaceAll(s, boldPlaceholder, "*")
	return s
}

// markdownToMrkdwn converts standard Markdown to Slack mrkdwn format.
// Code blocks and inline code are preserved verbatim.
func markdownToMrkdwn(s string) string {
	segments := splitCodeSegments(s)
	var b strings.Builder
	for _, seg := range segments {
		if seg.isCode {
			b.WriteString(seg.text)
		} else {
			b.WriteString(transformMarkdown(seg.text))
		}
	}
	return b.String()
}
