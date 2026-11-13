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

func TestContentScore(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"four latin words", "one two three four", 8},
		{"five latin words", "one two three four five", 10},
		{"four single-letter words", "i a m ok", 8},
		{"five single-letter words", "i a m ok go", 10},
		{"four numbers", "100 250 300 400", 8},
		{"five numbers", "100 250 300 400 500", 10},
		{"nine cjk", "一二三四五六七八九", 9},
		{"ten cjk", "一二三四五六七八九十", 10},
		{"nine cjk sentence", "这个方案还要再讨论", 9},
		{"ten cjk sentence", "这个方案还需要再讨论", 10},
		{"mixed reaches ten", "明天 meeting 改到下午三点", 10},
		{"mixed below ten", "看下 documentation 谢谢", 6},
		{"price with four words", "100 usd per month", 8},
		{"pure url", "https://example.com/a/b", 0},
		{"url with one cjk", "看 https://example.com/a/b", 1},
		{"url with enough cjk", "请看这个内容真的很有意思 https://example.com/a/b", 12},
		{"emoji only", "👍👍👍👍", 0},
		{"punctuation only", "！！！？？", 0},
		{"short cyrillic", "привет мир", 9},
		{"long cyrillic", "привет мир как", 12},
		{"short arabic", "مرحبا", 5},
		{"long arabic", "مرحبا بالعالم", 12},
		{"latin and cyrillic", "hello привет", 8},
		{"latin words and cyrillic", "hello hello привет", 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ContentScore(tt.input); got != tt.want {
				t.Errorf("ContentScore(%q) = %d, want %d", tt.input, got, tt.want)
			}
			if got, want := HasEnoughContent(tt.input), tt.want >= DefaultMinScore; got != want {
				t.Errorf("HasEnoughContent(%q) = %v, want %v", tt.input, got, want)
			}
		})
	}
}
