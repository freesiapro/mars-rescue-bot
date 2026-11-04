// Package textnorm 对文本做去重前的归一化。
// 归一化只用于生成内容键，原文不会写入数据库。
package textnorm

import (
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// DefaultMinRunes 是文本参与去重所需的最小有效字符数（不含 URL、空白和标点）。
const DefaultMinRunes = 4

var urlPattern = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)

const urlTrailingPunct = ".,;:!?)]}，。；：！？、）】」』"

// Normalize 返回用于去重的内容表示：
//   - Unicode NFKC，统一全角/半角；
//   - 去除零宽字符、变体选择符和软连字符；
//   - 折叠空白；
//   - 非 URL 部分做 ASCII 大小写折叠并去除标点/符号；
//   - URL 只规范化 scheme/host、默认端口、fragment、末尾斜杠和追踪参数。
func Normalize(input string) string {
	s := norm.NFKC.String(input)
	s = stripInvisible(s)
	s = collapseSpaces(s)

	var b strings.Builder
	last := 0
	for _, loc := range urlPattern.FindAllStringIndex(s, -1) {
		b.WriteString(normalizePlain(s[last:loc[0]]))
		raw := strings.TrimRight(s[loc[0]:loc[1]], urlTrailingPunct)
		if canonical, ok := canonicalURL(raw); ok {
			b.WriteString(" ")
			b.WriteString(canonical)
			b.WriteString(" ")
		} else {
			b.WriteString(normalizePlain(raw))
		}
		last = loc[1]
	}
	b.WriteString(normalizePlain(s[last:]))

	return collapseSpaces(strings.TrimSpace(b.String()))
}

// HasContent 报告归一化结果中是否至少有 minRunes 个非 URL 的字母或数字。
// 纯 URL、纯表情、纯标点都会返回 false。
func HasContent(normalized string, minRunes int) bool {
	if minRunes <= 0 {
		return strings.TrimSpace(normalized) != ""
	}
	stripped := urlPattern.ReplaceAllString(normalized, " ")
	count := 0
	for _, r := range stripped {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			count++
			if count >= minRunes {
				return true
			}
		}
	}
	return false
}

// IsRepeatedSingleRune 报告归一化文本是否由同一个字符重复组成。
// 标点已在 Normalize 中删除；空白也会参与比较，因此含空白的文本不算。
func IsRepeatedSingleRune(normalized string) bool {
	var first rune
	found := false
	for _, r := range normalized {
		if !found {
			first = r
			found = true
			continue
		}
		if r != first {
			return false
		}
	}
	return found
}

func normalizePlain(s string) string {
	s = asciiLower(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), unicode.IsMark(r), unicode.IsSpace(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

func canonicalURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", false
	}

	u.Scheme = strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	} else {
		u.Host = host
	}

	u.Fragment = ""
	if u.Path != "" {
		u.Path = strings.TrimRight(u.Path, "/")
	}

	query := u.Query()
	for key := range query {
		if isTrackingParam(key) {
			query.Del(key)
		}
	}
	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(query))
	for _, key := range keys {
		for _, value := range query[key] {
			parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(value))
		}
	}
	u.RawQuery = strings.Join(parts, "&")

	return u.String(), true
}

func isTrackingParam(key string) bool {
	key = strings.ToLower(key)
	switch key {
	case "fbclid", "gclid", "spm", "from", "share_source":
		return true
	}
	return strings.HasPrefix(key, "utm_")
}

func stripInvisible(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == 0x00AD: // soft hyphen
			continue
		case r >= 0x200B && r <= 0x200F: // zero-width and direction marks
			continue
		case r >= 0x202A && r <= 0x202E: // bidi embedding/override
			continue
		case r >= 0x2060 && r <= 0x2064: // word joiner and invisible operators
			continue
		case r == 0xFEFF: // BOM / zero-width no-break space
			continue
		case r >= 0xFE00 && r <= 0xFE0F: // variation selectors
			continue
		case r >= 0xE0100 && r <= 0xE01EF: // variation selectors supplement
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func collapseSpaces(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !space {
				b.WriteByte(' ')
				space = true
			}
			continue
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

func asciiLower(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}
