// Tests for bodylimit.go: JSON body size limit + multi-doc reject.
package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type bodyLimitTestStruct struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

// TestDecodeJSONBody_OK 正常 JSON 解码。
func TestDecodeJSONBody_OK(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"alice","age":30}`))
	var got bodyLimitTestStruct
	if !decodeJSONBody(w, r, &got) {
		t.Fatalf("decodeJSONBody returned false; body=%s", w.Body.String())
	}
	if got.Name != "alice" || got.Age != 30 {
		t.Fatalf("decoded wrong: %+v", got)
	}
}

// TestDecodeJSONBody_TooLarge 超过 1MB body 返 413。
func TestDecodeJSONBody_TooLarge(t *testing.T) {
	// 构造一个 > 1MB 的合法 JSON：{"name":"aaaa...aaaa"}
	big := strings.Repeat("a", maxJSONBodyBytes+1)
	body := `{"name":"` + big + `","age":1}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()
	var got bodyLimitTestStruct
	if decodeJSONBody(w, r, &got) {
		t.Fatalf("decodeJSONBody should return false for too-large body")
	}
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
	if !strings.Contains(w.Body.String(), "body_too_large") {
		t.Fatalf("body missing body_too_large: %s", w.Body.String())
	}
}

// TestDecodeJSONBody_Empty 空 body 返 400。
func TestDecodeJSONBody_Empty(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	var got bodyLimitTestStruct
	if decodeJSONBody(w, r, &got) {
		t.Fatalf("decodeJSONBody should return false for empty body")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// TestDecodeJSONBody_Invalid 非法 JSON 返 400。
func TestDecodeJSONBody_Invalid(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("not json"))
	var got bodyLimitTestStruct
	if decodeJSONBody(w, r, &got) {
		t.Fatalf("decodeJSONBody should return false for invalid JSON")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "invalid_json") {
		t.Fatalf("body missing invalid_json: %s", w.Body.String())
	}
}

// TestDecodeJSONBody_UnknownField 未知字段被拒绝（DisallowUnknownFields）。
func TestDecodeJSONBody_UnknownField(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"alice","sneaky":1}`))
	var got bodyLimitTestStruct
	if decodeJSONBody(w, r, &got) {
		t.Fatalf("decodeJSONBody should reject unknown fields")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// TestDecodeJSONBody_MultiDocument 拒绝 multi-document JSON（"{} {}" 攻击）。
func TestDecodeJSONBody_MultiDocument(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"alice"} {"age":2}`))
	var got bodyLimitTestStruct
	if decodeJSONBody(w, r, &got) {
		t.Fatalf("decodeJSONBody should reject multi-document JSON")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "extra JSON document") {
		t.Fatalf("body missing extra-doc message: %s", w.Body.String())
	}
}
