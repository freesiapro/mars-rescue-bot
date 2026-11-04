package textnorm

import "testing"

func TestNormalizeFoldsWidthCaseAndWhitespace(t *testing.T) {
	got := Normalize("  Ｈｅｌｌｏ，" + string(rune(0x200B)) + "  ＭＡＲＳ！  ")
	want := "hello mars"
	if got != want {
		t.Errorf("Normalize() = %q, want %q", got, want)
	}
}

func TestNormalizeRemovesPunctuationButKeepsLetters(t *testing.T) {
	got := Normalize("你好，世界！ Hello, world...")
	want := "你好世界 hello world"
	if got != want {
		t.Errorf("Normalize() = %q, want %q", got, want)
	}
}

func TestNormalizeCanonicalizesURLs(t *testing.T) {
	got := Normalize("look https://Example.COM:443/a/b/?utm_source=x&id=1&fbclid=y#frag ok")
	want := "look https://example.com/a/b?id=1 ok"
	if got != want {
		t.Errorf("Normalize() = %q, want %q", got, want)
	}
}

func TestNormalizePreservesURLPathCase(t *testing.T) {
	got := Normalize("https://Example.com/A/B")
	want := "https://example.com/A/B"
	if got != want {
		t.Errorf("Normalize() = %q, want %q", got, want)
	}
}

func TestNormalizeDropsDefaultPortAndTrailingPunctuation(t *testing.T) {
	got := Normalize("see http://example.com:80/x, thanks")
	want := "see http://example.com/x thanks"
	if got != want {
		t.Errorf("Normalize() = %q, want %q", got, want)
	}
}

func TestNormalizeSortsQueryAndDropsTrackingParams(t *testing.T) {
	got := Normalize("https://example.com/?b=2&utm_medium=social&a=1")
	want := "https://example.com?a=1&b=2"
	if got != want {
		t.Errorf("Normalize() = %q, want %q", got, want)
	}
}

func TestNormalizeHidesDifferenceInTrackingParams(t *testing.T) {
	a := Normalize("请看这个 https://example.com/post?utm_source=a")
	b := Normalize("请看这个 https://example.com/post?utm_source=b")
	if a != b {
		t.Errorf("tracking params changed key: %q != %q", a, b)
	}
}

func TestHasContent(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		minRunes int
		want     bool
	}{
		{"enough letters", "hello", DefaultMinRunes, true},
		{"enough cjk", "你好世界", DefaultMinRunes, true},
		{"too short", "abc", DefaultMinRunes, false},
		{"pure url", "https://example.com/a/b", DefaultMinRunes, false},
		{"url with too little text", "看 https://example.com/a/b", DefaultMinRunes, false},
		{"url with enough text", "请看这个 https://example.com/a/b", DefaultMinRunes, true},
		{"emoji only", "👍👍👍👍", DefaultMinRunes, false},
		{"punctuation only", "！！！？？", DefaultMinRunes, false},
		{"zero means non-empty", "", 0, false},
		{"zero accepts text", "x", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasContent(tt.input, tt.minRunes); got != tt.want {
				t.Errorf("HasContent(%q, %d) = %v, want %v", tt.input, tt.minRunes, got, tt.want)
			}
		})
	}
}
