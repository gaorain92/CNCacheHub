import { createApp } from 'vue'
import { createPinia } from 'pinia'
import ElementPlus from 'element-plus'
import * as ElementPlusIconsVue from '@element-plus/icons-vue'

import App from './App.vue'
import router from './router'
import { setOnUnauthorized } from './api/client'
import { useAuthStore } from './stores/auth'
import 'element-plus/dist/index.css'
import 'element-plus/theme-chalk/dark/css-vars.css'
import './styles/element-plus-dark.css'
import './style.css'

const app = createApp(App)
const pinia = createPinia()

app.use(pinia)
app.use(router)
app.use(ElementPlus)

// Register all Element Plus icons globally so views can use <el-icon><Cpu /></el-icon>
for (const [name, component] of Object.entries(ElementPlusIconsVue)) {
  app.component(name, component as never)
}

// 全局 401 拦截：清本地 user 状态 + 跳 /login。
//
// 注意：必须在 pinia 注册后才能 useAuthStore。
setOnUnauthorized(() => {
  const auth = useAuthStore()
  auth.clearLocal()
  if (router.currentRoute.value.path !== '/login' && router.currentRoute.value.path !== '/init') {
    router.replace({
      path: '/login',
      query: { redirect: router.currentRoute.value.fullPath },
    })
  }
})

app.mount('#app')
