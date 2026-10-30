package wikilink

import (
	"reflect"
	"testing"

	"golang.org/x/text/unicode/norm"
)

func TestCodeSpans(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"fenced block", "before\n```\n[[x]]\n```\nafter", 1},
		{"inline code", "text `[[x]]` more", 1},
		{"double backtick", "text ``[[x]]`` more", 1},
		{"no code", "plain [[x]] text", 0},
		{"unclosed fence", "```\n[[x]]\nno close", 1},
		{"tilde fence", "~~~\n[[x]]\n~~~", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if spans := codeSpans(tt.body); len(spans) != tt.want {
				t.Errorf("codeSpans() = %v, want %d spans", spans, tt.want)
			}
		})
	}
}

func TestFind(t *testing.T) {
	body := "a [[t|disp]] b [[h#sec]] ![[img.png]] `[[code]]` [[ ]] [[t]]"
	var got []Match
	for _, m := range Find(body) {
		got = append(got, Match{Target: m.Target, Display: m.Display, Embed: m.Embed})
	}
	want := []Match{
		{Target: "t", Display: "disp"},
		{Target: "h", Display: "h#sec"},
		{Target: "img.png", Display: "img.png", Embed: true},
		{Target: "t", Display: "t"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Find() = %+v, want %+v", got, want)
	}
}

func TestSetResolve(t *testing.T) {
	nfd := norm.NFD.String("concept/성염색체.md")
	s := NewSet([]string{"concept/Alpha.md", "sources/src-page.md", nfd, "notes/plain"})
	tests := []struct {
		link string
		want string
	}{
		{"concept/Alpha", "concept/Alpha.md"},  // <link>.md
		{"src-page", "sources/src-page.md"},    // sources/<link>.md
		{"notes/plain", "notes/plain"},         // verbatim
		{"alpha", "concept/Alpha.md"},          // case-insensitive basename
		{norm.NFC.String("성염색체"), nfd},         // NFC link → NFD file name
		{norm.NFC.String("concept/성염색체"), nfd}, // NFC path form
		{"missing", ""},
	}
	for _, tt := range tests {
		if got := s.Resolve(tt.link); got != tt.want {
			t.Errorf("Resolve(%q) = %q, want %q", tt.link, got, tt.want)
		}
	}
}

func TestStripUnresolved(t *testing.T) {
	s := NewSet([]string{"concept/keep.md"})
	body := "see [[keep]], [[gone|표시]], [[gone2#h]] and [[gone]] again\n" +
		"`[[in-code]]` ![[missing.png]]\n"
	got, dropped := StripUnresolved(body, s)
	want := "see [[keep]], 표시, gone2#h and gone again\n" +
		"`[[in-code]]` ![[missing.png]]\n"
	if got != want {
		t.Fatalf("body =\n%q\nwant\n%q", got, want)
	}
	if !reflect.DeepEqual(dropped, []string{"gone", "gone2"}) {
		t.Fatalf("dropped = %v", dropped)
	}
}

func TestUnresolved(t *testing.T) {
	s := NewSet([]string{"a.md"})
	got := Unresolved("[[a]] [[b]] [[b|x]] ![[c.png]] [[d]]", s)
	if !reflect.DeepEqual(got, []string{"b", "d"}) {
		t.Fatalf("Unresolved = %v", got)
	}
}
