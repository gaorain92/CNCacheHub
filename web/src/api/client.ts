import axios, { type AxiosError, type AxiosInstance } from 'axios'

/**
 * 统一 axios 实例
 * - baseURL 走 VITE_API_BASE（开发态由 Vite 代理到 :8080）
 * - 失败统一 reject 带 code/message，便于上层 catch
 */
export const api: AxiosInstance = axios.create({
  baseURL: import.meta.env.VITE_API_BASE || '/api',
  timeout: 10_000,
  headers: {
    Accept: 'application/json',
  },
})

// 4xx / 5xx / 网络错误统一转成有 code + message 的 Error
api.interceptors.response.use(
  (response) => response,
  (error: AxiosError<{ error?: { code?: string; message?: string } }>) => {
    const status = error.response?.status
    const data = error.response?.data
    const code =
      data?.error?.code ??
      (status ? `HTTP_${status}` : 'NETWORK_ERROR')
    const message =
      data?.error?.message ??
      error.message ??
      '请求失败，请稍后重试'

    const wrapped = new Error(message) as Error & { code: string; status?: number }
    wrapped.code = code
    if (status) wrapped.status = status
    return Promise.reject(wrapped)
  },
)
