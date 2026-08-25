// Package api: 请求 body 大小限制辅助。
//
// 默认 `http.MaxBytesReader` 行为是读到上限就报错，handler 里要 err 检查；
// 简化提供 decodeJSONBody：超限返 413 + 写 error，handler 一行搞定。
//
// 限制原因：所有 API JSON body 应该很小（kb 级），不限制会被恶意 client 用
// 几 GB body DoS 内存。
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// maxJSONBodyBytes 单次 JSON 解码允许的最大字节数。
//
// 1MB 足够绝大多数 API（preheat task、resource rule、registry patch 等），
// 又能挡住明显 DoS。如果业务后续要支持更大 body，应单独在 handler 加 limit。
const maxJSONBodyBytes = 1 << 20 // 1 MiB

// decodeJSONBody 读取 r.Body（限制大小）+ JSON 解码到 dst。
//
// 行为：
//   - 成功：返回 nil，handler 继续。
//   - body 超 maxJSONBodyBytes：写 413 + error code `body_too_large`，handler 返。
//   - body 非合法 JSON：写 400 + `invalid_json`（message 透传 json 错误便于诊断），
//     handler 返。
//   - Content-Type 不是 application/json：仍允许解码（部分 client 不严格设头）；
//     实际项目里要不要严格校验，看需要。
//
// 用法：
//
//	var req patchReq
//	if !decodeJSONBody(w, r, &req) {
//	    return  // 已写响应
//	}
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	// http.MaxBytesReader 在超出时返回 *http.MaxBytesError（Go 1.19+）。
	// 我们用自定义 limit reader 简单实现，更明确。
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	dec := json.NewDecoder(r.Body)
	// 拒绝未知字段（防 client 错传字段污染）。如果 client 需要前向兼容，
	// 可以在调用方重设 dec.DisallowUnknownFields = false。
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		// 区分 body 超限 vs 格式错误
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large",
				fmt.Sprintf("request body exceeds %d bytes", maxJSONBodyBytes))
			return false
		}
		// 空 body：视为无效
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid_json", "empty request body")
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid_json", "invalid JSON: "+err.Error())
		return false
	}
	// 防 multi-document JSON 攻击（"{\"a\":1}\n{\"b\":2}"）。
	// 标准 Decoder 一次只读一个 document，但 io.EOF 后再调还会再读。
	// 这里强制只接受一个 document。
	if dec.More() {
		writeError(w, http.StatusBadRequest, "invalid_json", "unexpected extra JSON document")
		return false
	}
	return true
}
