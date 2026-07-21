import { api } from './client'

/**
 * 控制台鉴权 API 客户端（PRD §14.1）。
 *
 * 走 cookie 鉴权（cnsid），由后端 Set-Cookie，浏览器自动带；
 * 这里不用手加 Authorization header。
 */

export interface AuthUser {
  id: number
  username: string
  isAdmin: boolean
  mustChangePassword: boolean
  createdAt: number
  lastLoginAt: number
  disabled: boolean
}

export interface AuthSession {
  token: string
  userId: number
  createdAt: number
  expiresAt: number
  lastSeenAt: number
  ip: string
  userAgent: string
}

export interface LoginResponse {
  user: AuthUser
  session: AuthSession
  expiresAt: number
}

export interface MeResponse {
  user?: AuthUser
  authenticated: boolean
  mustChangePassword: boolean
  initRequired: boolean
}

export interface InitStatusResponse {
  initialized: boolean
  userCount: number
}

export interface InitRequest {
  username: string
  password: string
}

export interface LoginRequest {
  username: string
  password: string
}

export interface ChangePasswordRequest {
  oldPassword: string
  newPassword: string
}

/**
 * GET /api/auth/init-status
 * 公开。判断是否要走初始化向导。
 */
export async function getInitStatus(): Promise<InitStatusResponse> {
  const { data } = await api.get<InitStatusResponse>('/auth/init-status')
  return data
}

/**
 * POST /api/auth/init
 * 公开。仅当无 admin 时成功。成功后自动登录 + 写 cookie。
 */
export async function initAdmin(req: InitRequest): Promise<LoginResponse> {
  const { data } = await api.post<LoginResponse>('/auth/init', req)
  return data
}

/**
 * POST /api/auth/login
 */
export async function login(req: LoginRequest): Promise<LoginResponse> {
  const { data } = await api.post<LoginResponse>('/auth/login', req)
  return data
}

/**
 * POST /api/auth/logout
 */
export async function logout(): Promise<void> {
  await api.post('/auth/logout')
}

/**
 * GET /api/auth/me
 * 返回当前用户；未登录 200 + authenticated=false。
 */
export async function me(): Promise<MeResponse> {
  const { data } = await api.get<MeResponse>('/auth/me')
  return data
}

/**
 * POST /api/auth/change-password
 */
export async function changePassword(req: ChangePasswordRequest): Promise<void> {
  await api.post('/auth/change-password', req)
}
