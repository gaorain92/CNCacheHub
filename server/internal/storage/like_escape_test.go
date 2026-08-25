// Tests for like_escape.go: LIKE 通配符转义。
package storage

import (
	"strings"
	"testing"
)

func TestEscapeLike_NoSpecial(t *testing.T) {
	got := escapeLike("abc", 100)
	if got != "abc" {
		t.Errorf("escapeLike(abc) = %q, want abc", got)
	}
}

func TestEscapeLike_Percent(t *testing.T) {
	got := escapeLike("100%", 100)
	if got != `100\%` {
		t.Errorf("escapeLike(100%%) = %q, want 100\\%%", got)
	}
}

func TestEscapeLike_Underscore(t *testing.T) {
	got := escapeLike("a_b", 100)
	if got != `a\_b` {
		t.Errorf("escapeLike(a_b) = %q, want a\\_b", got)
	}
}

func TestEscapeLike_Backslash(t *testing.T) {
	got := escapeLike(`a\b`, 100)
	if got != `a\\b` {
		t.Errorf("escapeLike(a\\b) = %q, want a\\\\b", got)
	}
}

func TestEscapeLike_Combo(t *testing.T) {
	// 50%_off  →  50\%\_off
	got := escapeLike("50%_off", 100)
	want := `50\%\_off`
	if got != want {
		t.Errorf("escapeLike(50%%_off) = %q, want %q", got, want)
	}
}

func TestEscapeLike_Truncate(t *testing.T) {
	got := escapeLike(strings.Repeat("a", 200), 100)
	if len(got) != 100 {
		t.Errorf("escapeLen = %d, want 100", len(got))
	}
}

func TestLikePattern(t *testing.T) {
	got := likePattern("100%", 100)
	want := `%100\%%`
	if got != want {
		t.Errorf("likePattern(100%%) = %q, want %q", got, want)
	}
}

// TestEscapeLike_ReDoSStyleLikeAnd 安全：用户输 `%_%` 不会被解释为通配符。
// 即我们构造一个 pattern 用 ESCAPE 子句 + 通配符 query，应用到 SQLite 应该不返回所有行。
func TestEscapeLike_PatternIsLiteral(t *testing.T) {
	// 仅测试转义正确：escapeLike("%") 应返 "\%" 而非 "%"。
	// 这样配合 ESCAPE '\' 子句就只匹配字面 "%"。
	if escapeLike("%", 100) != `\%` {
		t.Errorf("escapeLike(%%) should be \\%%")
	}
	// 用户输入 "_" 不应该匹配任意单字符。
	if escapeLike("_", 100) != `\_` {
		t.Errorf("escapeLike(_) should be \\_")
	}
}
