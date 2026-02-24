package main

import (
	"testing"
)

func TestMarkdownToMrkdwn_Headers(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"# Title", "*Title*"},
		{"## Subtitle", "*Subtitle*"},
		{"### Deep heading", "*Deep heading*"},
		{"###### H6", "*H6*"},
		{"# Title\nsome text\n## Sub", "*Title*\nsome text\n*Sub*"},
	}
	for _, tt := range tests {
		got := markdownToMrkdwn(tt.in)
		if got != tt.want {
			t.Errorf("markdownToMrkdwn(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestMarkdownToMrkdwn_Bold(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"**bold text**", "*bold text*"},
		{"word **bold** word", "word *bold* word"},
	}
	for _, tt := range tests {
		got := markdownToMrkdwn(tt.in)
		if got != tt.want {
			t.Errorf("markdownToMrkdwn(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestMarkdownToMrkdwn_Italic(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"word *italic* word", "word _italic_ word"},
	}
	for _, tt := range tests {
		got := markdownToMrkdwn(tt.in)
		if got != tt.want {
			t.Errorf("markdownToMrkdwn(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestMarkdownToMrkdwn_BoldItalic(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"***bold italic***", "*_bold italic_*"},
	}
	for _, tt := range tests {
		got := markdownToMrkdwn(tt.in)
		if got != tt.want {
			t.Errorf("markdownToMrkdwn(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestMarkdownToMrkdwn_Links(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"[click here](https://example.com)", "<https://example.com|click here>"},
		{"see [docs](https://docs.io) for info", "see <https://docs.io|docs> for info"},
	}
	for _, tt := range tests {
		got := markdownToMrkdwn(tt.in)
		if got != tt.want {
			t.Errorf("markdownToMrkdwn(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestMarkdownToMrkdwn_Images(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"![alt text](https://img.png)", "<https://img.png|alt text>"},
	}
	for _, tt := range tests {
		got := markdownToMrkdwn(tt.in)
		if got != tt.want {
			t.Errorf("markdownToMrkdwn(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestMarkdownToMrkdwn_Strikethrough(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"~~deleted~~", "~deleted~"},
		{"keep ~~removed~~ keep", "keep ~removed~ keep"},
	}
	for _, tt := range tests {
		got := markdownToMrkdwn(tt.in)
		if got != tt.want {
			t.Errorf("markdownToMrkdwn(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestMarkdownToMrkdwn_CodeBlockPreserved(t *testing.T) {
	in := "before\n```go\nfunc **notBold**() {}\n```\nafter **bold**"
	want := "before\n```go\nfunc **notBold**() {}\n```\nafter *bold*"
	got := markdownToMrkdwn(in)
	if got != want {
		t.Errorf("markdownToMrkdwn(code block) =\n%s\nwant:\n%s", got, want)
	}
}

func TestMarkdownToMrkdwn_InlineCodePreserved(t *testing.T) {
	in := "use `**bold**` for bold and **actual bold**"
	want := "use `**bold**` for bold and *actual bold*"
	got := markdownToMrkdwn(in)
	if got != want {
		t.Errorf("markdownToMrkdwn(inline code) = %q, want %q", got, want)
	}
}

func TestMarkdownToMrkdwn_MixedContent(t *testing.T) {
	in := `# Plan

## Step 1: Update the API

Modify **handler.go** to add the new endpoint. See [RFC](https://example.com/rfc) for details.

` + "```go\nfunc NewHandler() {\n\t// **important**\n}\n```" + `

## Step 2: Tests

Add *unit tests* for the handler. Remove ~~old tests~~.`

	want := `*Plan*

*Step 1: Update the API*

Modify *handler.go* to add the new endpoint. See <https://example.com/rfc|RFC> for details.

` + "```go\nfunc NewHandler() {\n\t// **important**\n}\n```" + `

*Step 2: Tests*

Add _unit tests_ for the handler. Remove ~old tests~.`

	got := markdownToMrkdwn(in)
	if got != want {
		t.Errorf("markdownToMrkdwn(mixed) =\n%s\n\nwant:\n%s", got, want)
	}
}

func TestSplitCodeSegments(t *testing.T) {
	in := "text `code` more ```\nblock\n``` end"
	segs := splitCodeSegments(in)

	// Expect: "text " (text), "`code`" (code), " more " (text), "```\nblock\n```" (code), " end" (text)
	if len(segs) != 5 {
		t.Fatalf("got %d segments, want 5: %+v", len(segs), segs)
	}
	if segs[0].isCode || segs[0].text != "text " {
		t.Errorf("seg 0: %+v", segs[0])
	}
	if !segs[1].isCode || segs[1].text != "`code`" {
		t.Errorf("seg 1: %+v", segs[1])
	}
	if segs[2].isCode || segs[2].text != " more " {
		t.Errorf("seg 2: %+v", segs[2])
	}
	if !segs[3].isCode {
		t.Errorf("seg 3 should be code: %+v", segs[3])
	}
	if segs[4].isCode || segs[4].text != " end" {
		t.Errorf("seg 4: %+v", segs[4])
	}
}

func TestMarkdownToMrkdwn_NoChange(t *testing.T) {
	// Already valid mrkdwn or plain text should pass through.
	in := "plain text with no markdown"
	got := markdownToMrkdwn(in)
	if got != in {
		t.Errorf("markdownToMrkdwn(%q) = %q, want unchanged", in, got)
	}
}
