package bot

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/telebot.v4"
)

const linkContentKeyPrefix = "link:v1:"

// linkPattern 与 textnorm 里的 URL 识别保持一致。这里单独保留一份，
// 是为了拿到字节位置，用于判断链接是否被 url / text_link 实体覆盖。
var linkPattern = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)

// 与 textnorm.Normalize 相同的句末标点集合。
const linkTrailingPunct = ".,;:!?)]}，。；：！？、）】」』"

// messageLinkURLs 判断整条消息是否只有链接，并返回按出现顺序排列的链接。
//
// 纯链接包括：
//   - 文本里的一个或多个 http(s) URL，且没有其它文字；
//   - 被 url 实体覆盖的文本（URL 本身来自被覆盖的文本）；
//   - 整条文本都被 text_link 实体覆盖（URL 取实体的 url 字段）。
//
// 空白、标点、符号（包括 emoji）不算“其它文字”；只要链接之外还有字母或
// 数字，就返回 false，让消息继续走原有文本规则。text_link 的锚文本无论写了
// 什么，只要没有其它未覆盖文字，都算链接消息，因为链接目标在 url 字段里。
func messageLinkURLs(msg *telebot.Message) ([]string, bool) {
	if msg == nil {
		return nil, false
	}

	raw := msg.Text
	var entities telebot.Entities
	if msg.RichMessage != nil {
		raw = messagePlainText(msg)
	} else {
		entities = msg.Entities
	}
	if strings.TrimSpace(raw) == "" {
		return nil, false
	}

	runes := []rune(raw)
	unitToRune := utf16UnitToRuneIndex(runes)
	covered := make([]bool, len(runes))
	var links []collectedLink

	for _, entity := range entities {
		if entity.Type != telebot.EntityURL && entity.Type != telebot.EntityTextLink {
			continue
		}
		start, end := entityRuneRange(entity, len(runes), unitToRune)
		if start >= end {
			continue
		}
		var url string
		if entity.Type == telebot.EntityTextLink {
			url = strings.TrimSpace(entity.URL)
		} else {
			url = strings.TrimSpace(string(runes[start:end]))
		}
		if url == "" {
			continue
		}
		links = append(links, collectedLink{start: start, url: url})
		for i := start; i < end; i++ {
			covered[i] = true
		}
	}

	for _, loc := range linkPattern.FindAllStringIndex(raw, -1) {
		match := raw[loc[0]:loc[1]]
		trimmed := strings.TrimRight(match, linkTrailingPunct)
		if trimmed == "" {
			continue
		}
		start := utf8.RuneCountInString(raw[:loc[0]])
		end := start + utf8.RuneCountInString(trimmed)
		if anyCovered(covered, start, end) {
			continue
		}
		links = append(links, collectedLink{start: start, url: trimmed})
		for i := start; i < end; i++ {
			covered[i] = true
		}
	}

	if len(links) == 0 {
		return nil, false
	}
	for i, r := range runes {
		if !covered[i] && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return nil, false
		}
	}

	sort.SliceStable(links, func(i, j int) bool { return links[i].start < links[j].start })
	urls := make([]string, len(links))
	for i := range links {
		urls[i] = links[i].url
	}
	return urls, true
}

func isPureLinkMessage(msg *telebot.Message) bool {
	_, ok := messageLinkURLs(msg)
	return ok
}

type collectedLink struct {
	start int
	url   string
}

// utf16UnitToRuneIndex 把 UTF-16 码元偏移映射到 rune 下标。
// 实体 Offset/Length 以 UTF-16 计数，emoji 等非 BMP 字符占两个码元。
func utf16UnitToRuneIndex(runes []rune) []int {
	units := 0
	for _, r := range runes {
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	mapping := make([]int, units+1)
	unitIndex, runeIndex := 0, 0
	for _, r := range runes {
		mapping[unitIndex] = runeIndex
		if r > 0xFFFF {
			mapping[unitIndex+1] = runeIndex
			unitIndex += 2
		} else {
			unitIndex++
		}
		runeIndex++
	}
	mapping[unitIndex] = len(runes)
	return mapping
}

func entityRuneRange(entity telebot.MessageEntity, runeCount int, unitToRune []int) (int, int) {
	if entity.Offset < 0 || entity.Length <= 0 || entity.Offset >= len(unitToRune)-1 {
		return 0, 0
	}
	endUnit := entity.Offset + entity.Length
	if endUnit > len(unitToRune)-1 {
		endUnit = len(unitToRune) - 1
	}
	start := unitToRune[entity.Offset]
	end := unitToRune[endUnit]
	if start < 0 {
		start = 0
	}
	if end > runeCount {
		end = runeCount
	}
	return start, end
}

func anyCovered(covered []bool, start, end int) bool {
	if start < 0 {
		start = 0
	}
	if end > len(covered) {
		end = len(covered)
	}
	for i := start; i < end; i++ {
		if covered[i] {
			return true
		}
	}
	return false
}

// canonicalLink 只做最保守的链接规范化：scheme 和 host 转小写。
// path、query、fragment、末尾斜杠、追踪参数、端口、默认/非默认 scheme
// 全部保留；有 scheme 与无 scheme 的文本不做合并。拿不准的差异宁可
// 视为不同链接，避免把用户眼中的不同链接误判为重复。
func canonicalLink(raw string) string {
	s := strings.TrimSpace(raw)
	sep := strings.Index(s, "://")
	if sep <= 0 {
		return s
	}
	scheme := strings.ToLower(s[:sep])
	rest := s[sep+3:]
	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}
	authority := rest[:end]
	userinfo := ""
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		userinfo = authority[:at+1]
		authority = authority[at+1:]
	}
	return scheme + "://" + userinfo + strings.ToLower(authority) + rest[end:]
}

// linkContentKey 生成链接内容键。多个链接按消息中的出现顺序用换行连接，
// 不排序、不去重：顺序不同或重复次数不同都会得到不同键。
// URL 先做 canonicalLink 的最小规范化，再对连接结果做 HMAC-SHA256，
// 数据库只保存键，不保存链接原文。
func linkContentKey(secret string, urls []string) string {
	parts := make([]string, len(urls))
	for i, url := range urls {
		parts[i] = canonicalLink(url)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strings.Join(parts, "\n")))
	return linkContentKeyPrefix + hex.EncodeToString(mac.Sum(nil))
}
