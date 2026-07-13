import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    redirect: '/dashboard',
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
    path: '/logs',
    name: 'logs',
    component: () => import('@/views/LogsView.vue'),
    meta: { title: '请求日志' },
  },
  {
    path: '/:pathMatch(.*)*',
    name: 'not-found',
    component: () => import('@/views/NotFoundView.vue'),
    meta: { title: '页面未找到' },
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

export default router
