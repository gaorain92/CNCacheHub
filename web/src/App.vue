<script setup lang="ts">
import { computed, onBeforeUnmount, watch } from 'vue'
import { useRoute } from 'vue-router'
import AppLayout from '@/layouts/AppLayout.vue'
import { useAuthStore } from '@/stores/auth'
import { useHealthStore } from '@/stores/health'

const auth = useAuthStore()
const health = useHealthStore()
const route = useRoute()

// 公开页面（login / init / not-found）不显示侧栏
const useBareLayout = computed(() => {
  if (route.meta?.public) return true
  if (!auth.isAuthenticated) return true
  return false
})

// 全局后端健康检查 — 跟路由解耦，任何页面 mount 后都能看到正确状态点。
// 用 watch 跟踪 isAuthenticated：登录成功的瞬间立即 fetch（避免 onMounted
// 时序竞争 — App mount 早于 router guard 完成的 bootstrap）。
let healthTimer: ReturnType<typeof setInterval> | null = null
let lastAuthed = false
watch(
  () => auth.isAuthenticated,
  (authed) => {
    if (authed && !lastAuthed) {
      // 从未登录 → 登录：立即拉一次
      health.fetch()
    }
    if (authed) {
      // 已登录：保证 30s 轮询在跑
      if (!healthTimer) {
        healthTimer = setInterval(() => {
          if (auth.isAuthenticated) health.fetch()
        }, 30_000)
      }
    } else {
      // 登出：清掉轮询
      if (healthTimer) {
        clearInterval(healthTimer)
        healthTimer = null
      }
    }
    lastAuthed = authed
  },
  { immediate: true }
)
onBeforeUnmount(() => {
  if (healthTimer) clearInterval(healthTimer)
})
</script>

<template>
  <router-view v-if="useBareLayout" />
  <AppLayout v-else>
    <router-view v-slot="{ Component }">
      <transition name="rise" mode="out-in">
        <component :is="Component" />
      </transition>
    </router-view>
  </AppLayout>
</template>

<style>
.rise-enter-active,
.rise-leave-active {
  transition: opacity 0.22s ease, transform 0.22s ease;
}
.rise-enter-from {
  opacity: 0;
  transform: translateY(8px);
}
.rise-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}
</style>
