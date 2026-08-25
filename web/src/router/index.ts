import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    redirect: '/dashboard',
  },
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/LoginView.vue'),
    meta: { title: '登录', public: true },
  },
  {
    path: '/init',
    name: 'init',
    component: () => import('@/views/InitView.vue'),
    meta: { title: '初始化', public: true, initOnly: true },
  },
  {
    path: '/dashboard',
    name: 'dashboard',
    component: () => import('@/views/DashboardView.vue'),
    meta: { title: '总览仪表盘' },
  },
  {
    path: '/docker',
    name: 'docker',
    component: () => import('@/views/DockerView.vue'),
    meta: { title: 'Docker 加速' },
  },
  {
    path: '/steamcmd',
    name: 'steamcmd',
    component: () => import('@/views/SteamCMDView.vue'),
    meta: { title: 'SteamCMD 加速' },
  },
  {
    path: '/clients',
    name: 'clients',
    component: () => import('@/views/ClientsView.vue'),
    meta: { title: '客户端配置' },
  },
  {
    path: '/preheat',
    name: 'preheat',
    component: () => import('@/views/PreheatView.vue'),
    meta: { title: '预热任务' },
  },
  {
    path: '/diagnostics',
    name: 'diagnostics',
    component: () => import('@/views/DiagnosticsView.vue'),
    meta: { title: '诊断中心' },
  },
  {
    path: '/cache',
    name: 'cache',
    component: () => import('@/views/CacheView.vue'),
    meta: { title: '缓存管理' },
  },
  {
    path: '/settings',
    name: 'settings',
    component: () => import('@/views/SettingsView.vue'),
    meta: { title: '系统设置' },
  },
  {
    path: '/resources',
    name: 'resources',
    component: () => import('@/views/ResourcesView.vue'),
    meta: { title: '资源加速中心' },
  },
  {
    path: '/huggingface',
    name: 'huggingface',
    component: () => import('@/views/HuggingFaceView.vue'),
    meta: { title: 'HuggingFace 模型' },
  },
  {
    path: '/logs',
    name: 'logs',
    component: () => import('@/views/LogsView.vue'),
    meta: { title: '请求日志' },
  },
  {
    path: '/:pathMatch(.*)*',
    name: 'not-found',
    component: () => import('@/views/NotFoundView.vue'),
    meta: { title: '页面未找到', public: true },
  },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
  scrollBehavior() {
    return { top: 0 }
  },
})

// 同步 document.title
router.afterEach((to) => {
  const title = (to.meta?.title as string | undefined) ?? ''
  document.title = title ? `${title} · CNCacheHub` : 'CNCacheHub 控制台'
})

// 全局守卫：未登录 → /login；已登录访问 /login → /dashboard；
// 未初始化访问受保护路由 → /init；已初始化访问 /init → /login。
router.beforeEach(async (to) => {
  const auth = useAuthStore()
  // 首次进任意路由时拉一次 init 状态
  if (auth.user === null && !auth.initialized && !auth.loading) {
    await auth.bootstrap()
  }

  // 公开路由直接放行
  if (to.meta?.public) {
    // 已登录访问 /login → 跳 dashboard
    if (to.path === '/login' && auth.isAuthenticated) {
      return { path: '/dashboard' }
    }
    return true
  }

  // /init 路由：仅在未初始化时可访问
  if (to.path === '/init') {
    if (auth.initialized) {
      return { path: '/login' }
    }
    return true
  }

  // 受保护路由：未登录 → /login
  if (!auth.isAuthenticated) {
    return { path: '/login', query: { redirect: to.fullPath } }
  }

  return true
})

export default router
