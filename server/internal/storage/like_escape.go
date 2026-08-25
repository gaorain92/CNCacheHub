// Package storage: LIKE 子句通配符转义。
//
// 问题：LIKE 子句里 `%` 是通配符，匹配任意字符；`_` 匹配单个字符。
// 用户输入如果直接拼到 LIKE pattern 里，会被解释成通配符：
//
//   - 用户搜 "abc%" → 实际匹配 "abc" + 任意字符
//   - 用户搜 "ab_c" → 实际匹配 "ab?c"（中间任意一个字符）
//
// 这本身不是 SQL injection（参数化已经防止），但会让搜索结果
// "多出"用户没明确要求的内容。极端情况：
//   - 用户搜 "%" → 匹配所有（信息泄露：能 enumerate 全表）
//   - 用户搜 "_%" → 匹配所有（类似）
//
// 修法：把用户输入里的 `%` `_` `\` 转义为 `\%` `\_` `\\`，再
// 用 ESCAPE '\\' 子句告诉 SQLite 这些前缀是字面量。
package storage

import "strings"

// escapeLike 转义用户输入里的 LIKE 通配符。
//
// 返回值已经包含前后的 `%...%`（如果 wantPrefixSuffix=true）或原值（=精确匹配）。
//
// 防御：同时也限制 maxLen 防止滥用。
func escapeLike(s string, maxLen int) string {
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	// 必须先转义 \（避免重复转义），再转义 % _
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// likePattern 构造 LIKE pattern（带前/后 %）。
//
// 用法：pattern := likePattern(query, 100) → 在 SQL 里用 `LIKE ? ESCAPE '\\'`
//
// 返回值示例：
//   - escapeLike("ab%c", 100)  → "ab\\%c"
//   - likePattern("ab%c", 100) → "%ab\\%c%"
func likePattern(s string, maxLen int) string {
	return "%" + escapeLike(s, maxLen) + "%"
}

// maxSearchQueryLen 用户搜索框输入的最大长度（防 DoS / 异常大 body）。
const maxSearchQueryLen = 100
