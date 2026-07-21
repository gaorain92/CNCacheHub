import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import * as authApi from '@/api/auth'
import type { AuthUser } from '@/api/auth'

/**
 * 控制台鉴权 store。
 *
 * 状态：
 *   - user: 当前登录用户（null = 未登录）
 *   - initialized: 是否已初始化（无 admin 时为 false）
 *   - loading: 初始化检查中
 *   - mustChangePassword: 强制改密标志
 *
 * 行为：
 *   - bootstrap() 启动时调一次，决定跳 login / init / dashboard
 *   - login() 成功后刷新 user
 *   - logout() 清状态
 *   - 401 全局拦截：api 拦截器会调 onUnauthorized() 跳 /login
 */
export const useAuthStore = defineStore('auth', () => {
  const user = ref<AuthUser | null>(null)
  const initialized = ref(false)
  const loading = ref(false)
  const mustChangePassword = ref(false)
  const errorMessage = ref('')

  const isAuthenticated = computed(() => user.value !== null)
  const isAdmin = computed(() => user.value?.isAdmin === true)
  const username = computed(() => user.value?.username ?? '')

  /**
   * 启动时调一次：检查 init 状态 + 当前登录态。
   */
  async function bootstrap(): Promise<{ initRequired: boolean; authenticated: boolean }> {
    loading.value = true
    errorMessage.value = ''
    try {
      const status = await authApi.getInitStatus()
      initialized.value = status.initialized
      // 即使未初始化也调 /me：可能未登录，但 initRequired 标志重要
      const me = await authApi.me()
      user.value = me.user ?? null
      mustChangePassword.value = me.mustChangePassword
      return {
        initRequired: me.initRequired,
        authenticated: me.authenticated,
      }
    } catch (e) {
      errorMessage.value = (e as Error).message
      return { initRequired: false, authenticated: false }
    } finally {
      loading.value = false
    }
  }

  async function login(usernameInput: string, password: string): Promise<boolean> {
    loading.value = true
    errorMessage.value = ''
    try {
      const resp = await authApi.login({ username: usernameInput, password })
      user.value = resp.user
      mustChangePassword.value = resp.user.mustChangePassword
      initialized.value = true
      return true
    } catch (e) {
      errorMessage.value = (e as Error).message
      return false
    } finally {
      loading.value = false
    }
  }

  async function initAdmin(usernameInput: string, password: string): Promise<boolean> {
    loading.value = true
    errorMessage.value = ''
    try {
      const resp = await authApi.initAdmin({ username: usernameInput, password })
      user.value = resp.user
      mustChangePassword.value = resp.user.mustChangePassword
      initialized.value = true
      return true
    } catch (e) {
      errorMessage.value = (e as Error).message
      return false
    } finally {
      loading.value = false
    }
  }

  async function logout(): Promise<void> {
    try {
      await authApi.logout()
    } catch {
      // ignore
    } finally {
      user.value = null
      mustChangePassword.value = false
    }
  }

  async function changePassword(oldPassword: string, newPassword: string): Promise<boolean> {
    loading.value = true
    errorMessage.value = ''
    try {
      await authApi.changePassword({ oldPassword, newPassword })
      if (user.value) {
        user.value.mustChangePassword = false
        mustChangePassword.value = false
      }
      return true
    } catch (e) {
      errorMessage.value = (e as Error).message
      return false
    } finally {
      loading.value = false
    }
  }

  function clearLocal(): void {
    user.value = null
    mustChangePassword.value = false
  }

  return {
    user,
    initialized,
    loading,
    mustChangePassword,
    errorMessage,
    isAuthenticated,
    isAdmin,
    username,
    bootstrap,
    login,
    initAdmin,
    logout,
    changePassword,
    clearLocal,
  }
})
