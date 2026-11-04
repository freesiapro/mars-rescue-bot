package bot

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	"gopkg.in/telebot.v4"
)

// MessagePresentFields 返回消息中非空的顶层字段名，已排序。
// 只返回字段名，不返回字段值，也不返回正文。
//
// 使用 telebot 已解析的 Message 结构，因此看不到 telebot 未建模的字段；
// 这是不再保存原始 update JSON 后的取舍。富文本路由用的空白占位 Text
// 会被排除，避免把内部占位符误报成消息字段。
func MessagePresentFields(msg *telebot.Message) []string {
	return messagePresentFields(msg)
}

func messagePresentFields(msg *telebot.Message) []string {
	if msg == nil {
		return nil
	}
	encoded, err := json.Marshal(msg)
	if err != nil {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil
	}
	if msg.RichMessage != nil && strings.TrimSpace(msg.Text) == "" {
		delete(fields, "text")
	}
	present := make([]string, 0, len(fields))
	for name, value := range fields {
		if jsonValuePresent(value) {
			present = append(present, name)
		}
	}
	sort.Strings(present)
	return present
}

// messageRichBlockTypes 返回富文本块树里出现过的 type，去重并排序。
// 只记录结构类型，不记录任何文本。使用 telebot 已解析的 RichMessage，
// 因此未建模的块类型不会出现在这里。
func messageRichBlockTypes(msg *telebot.Message) []string {
	if msg == nil || msg.RichMessage == nil {
		return nil
	}
	types := make(map[string]struct{})
	collectTypedBlockTypes(msg.RichMessage.Blocks, types)
	return sortedTypeNames(types)
}

func collectTypedBlockTypes(blocks []telebot.RichBlock, types map[string]struct{}) {
	for i := range blocks {
		block := &blocks[i]
		if block.Type != "" {
			types[block.Type] = struct{}{}
		}
		collectTypedBlockTypes(block.Blocks, types)
		for _, item := range block.Items {
			collectTypedBlockTypes(item.Blocks, types)
		}
	}
}

func sortedTypeNames(types map[string]struct{}) []string {
	if len(types) == 0 {
		return nil
	}
	names := make([]string, 0, len(types))
	for name := range types {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// jsonValuePresent 判断一个 JSON 值是否算“非空”：
// null、空字符串、空数组、空对象、false 和 0 都视为空。
// telebot 结构体里大量布尔/数字字段没有 omitempty，序列化后会把零值
// 带出来；这里统一过滤，避免日志出现几十个噪声字段。
//
// 对象会递归检查：telebot 还有一些没有 omitempty 的嵌套结构体（例如
// chat_background_set、paid_media），零值也会序列化成非空对象。只要对象
// 里所有叶子都是空值，整个字段就算空。数组只要非空就算存在，避免丢到
// 只含空块的合法数组字段。
func jsonValuePresent(raw json.RawMessage) bool {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) {
		return false
	}
	switch value[0] {
	case '"':
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			return false
		}
		return text != ""
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(value, &items); err != nil {
			return false
		}
		return len(items) > 0
	case '{':
		var object map[string]json.RawMessage
		if err := json.Unmarshal(value, &object); err != nil {
			return false
		}
		for _, item := range object {
			if jsonValuePresent(item) {
				return true
			}
		}
		return false
	case 'f':
		return !bytes.Equal(value, []byte("false"))
	case 't':
		return true
	default:
		return !bytes.Equal(value, []byte("0")) && !bytes.Equal(value, []byte("0.0"))
	}
}
