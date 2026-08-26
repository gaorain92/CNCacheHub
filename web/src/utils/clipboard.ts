// utils/clipboard.ts — 健壮的剪贴板写入，兼容 HTTP / 非 secure context。
//
// 问题：`navigator.clipboard.writeText()` 只在 secure context（HTTPS /
// localhost / 127.0.0.1）下可用。CNCacheHub 的 web UI 默认部署在 HTTP
// 公网 IP（如 `http://your-host.example.com/`），secure context 不可用
// → `navigator.clipboard` 是 undefined 或 writeText 抛 "Document is not
// focused" / "Write permission denied"，导致所有 "复制" 按钮 catch 报
// "复制失败"。
//
// 修法：分级 fallback：
//   1. 优先 navigator.clipboard.writeText（HTTPS 站点直接走这条路）
//   2. 退化到 document.execCommand('copy') + 隐藏 textarea（HTTP 公网
//      IP 也能用 — 这是浏览器长期保留的同步 API）
//   3. 最后 window.prompt 弹窗让用户手动 Ctrl+C（最差兜底）
//
// 同步返回 boolean，让调用方决定是否提示"复制成功"。
export async function copyToClipboard(text: string): Promise<boolean> {
  // 1) 现代 API — secure context (HTTPS / localhost)
  if (typeof navigator !== 'undefined' && navigator.clipboard) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // 拒绝（无用户手势 / document 未 focus / 权限被禁）— 退化到下面
    }
  }

  // 2) 旧 API — 任意 context 都能用
  if (typeof document !== 'undefined') {
    try {
      const ta = document.createElement('textarea')
      ta.value = text
      // 放到屏幕外但保留可访问
      ta.style.position = 'fixed'
      ta.style.top = '0'
      ta.style.left = '0'
      ta.style.width = '1px'
      ta.style.height = '1px'
      ta.style.opacity = '0'
      ta.style.pointerEvents = 'none'
      document.body.appendChild(ta)
      ta.focus()
      ta.select()
      const ok = document.execCommand('copy')
      document.body.removeChild(ta)
      if (ok) return true
    } catch {
      // execCommand 在很老的浏览器可能 throw — 继续到 prompt
    }
  }

  // 3) 终极兜底：弹窗让用户手动复制
  if (typeof window !== 'undefined') {
    window.prompt('请手动复制（Ctrl+C / Cmd+C）：', text)
  }
  return false
}
