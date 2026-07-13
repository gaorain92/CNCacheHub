package api

import (
	"encoding/json"
	"net/http"
)

// encodeJSON 把 body 编码为 JSON 并写入 w（同时设置 status 与 Content-Type）。
//
// 行为：
//   - 显式设置 Content-Type（即使中间件已设）；
//   - 出错时仅返回 error，由调用方决定如何处理（不写 body）。
func encodeJSON(w http.ResponseWriter, status int, body any) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(body)
}
