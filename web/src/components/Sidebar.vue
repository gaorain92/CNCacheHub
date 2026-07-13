<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter, type RouteRecordName } from 'vue-router'
import {
  Odometer,
  Box,
  Goods,
  DocumentCopy,
  HotWater,
  FirstAidKit,
  DataLine,
  List,
} from '@element-plus/icons-vue'
import type { Component } from 'vue'

interface NavItem {
  name: RouteRecordName | null
  label: string
  path: string
  icon: Component
  badge?: string
}

const route = useRoute()
const router = useRouter()

const items: NavItem[] = [
  { name: 'dashboard', path: '/dashboard', label: '总览仪表盘', icon: Odometer },
  { name: 'docker', path: '/docker', label: 'Docker 加速', icon: Box },
  { name: 'steamcmd', path: '/steamcmd', label: 'SteamCMD 加速', icon: Goods },
  { name: 'clients', path: '/clients', label: '客户端配置', icon: DocumentCopy },
  { name: 'preheat', path: '/preheat', label: '预热任务', icon: HotWater },
  { name: 'diagnostics', path: '/diagnostics', label: '诊断中心', icon: FirstAidKit },
  { name: 'cache', path: '/cache', label: '缓存管理', icon: DataLine },
  { name: 'logs', path: '/logs', label: '请求日志', icon: List },
]

const activeName = computed(() => route.name)

function go(item: NavItem): void {
  router.push(item.path)
}
</script>

<template>
  <aside
    class="fixed left-5 top-5 bottom-5 w-[17.5rem] glass rounded-[2rem] p-5 flex flex-col z-20"
  >
    <!-- Logo -->
    <router-link
      to="/dashboard"
      class="flex items-center gap-3 px-3 py-3 rounded-2xl bg-white/[.04] border border-white/[.08] hover:bg-white/[.06] transition"
    >
      <div
        class="h-12 w-12 rounded-2xl bg-gradient-to-br from-mint to-violet flex items-center justify-center shadow-glow"
      >
        <el-icon :size="22" color="#020617"><Box /></el-icon>
      </div>
      <div class="leading-tight">
        <div class="text-lg font-semibold tracking-tight">CNCacheHub</div>
        <div class="text-xs text-slate-400">自托管下载加速中枢</div>
      </div>
    </router-link>

    <!-- Nav -->
    <nav class="mt-6 space-y-1.5 text-sm flex-1 overflow-y-auto scrollbar">
      <button
        v-for="item in items"
        :key="String(item.name)"
        type="button"
        :class="[
          'nav-item flex w-full items-center gap-3 rounded-2xl border border-transparent px-4 py-3 text-left transition',
          activeName === item.name ? 'active' : '',
        ]"
        @click="go(item)"
      >
        <el-icon :size="18" class="shrink-0">
          <component :is="item.icon" />
        </el-icon>
        <span class="flex-1">{{ item.label }}</span>
        <span
          v-if="item.badge"
          class="text-[10px] rounded-full bg-mint/20 text-mint px-2 py-0.5"
        >{{ item.badge }}</span>
      </button>
    </nav>

    <!-- Footer actions -->
    <div class="mt-4 space-y-3">
      <div
        class="rounded-3xl border border-mint/20 bg-mint/10 p-4"
      >
        <div class="flex items-center gap-2 text-sm font-medium text-mint">
          <span class="dot bg-mint animate-pulse" />
          公网安全提醒
        </div>
        <p class="mt-2 text-xs leading-5 text-slate-300">
          代理入口已开启 IP 白名单，Docker 只读 Token 已启用。
        </p>
      </div>
      <div class="grid grid-cols-2 gap-2 text-xs">
        <button
          type="button"
          class="btn rounded-2xl bg-white/[.05] px-3 py-2 text-slate-300 hover:bg-white/[.08] transition"
          disabled
        >
          初始化预览
        </button>
        <button
          type="button"
          class="btn rounded-2xl bg-white/[.05] px-3 py-2 text-slate-300 hover:bg-white/[.08] transition"
          disabled
        >
          登录页
        </button>
      </div>
    </div>
  </aside>
</template>

<style scoped>
.nav-item {
  color: #94a3b8;
  cursor: pointer;
}
.nav-item:hover:not(:disabled) {
  color: #f8fafc;
  background: rgba(148, 163, 184, 0.09);
  transform: translateX(2px);
}
.nav-item.active {
  color: #ecfeff;
  background: linear-gradient(90deg, rgba(45, 212, 191, 0.18), rgba(139, 92, 246, 0.1));
  border-color: rgba(45, 212, 191, 0.28);
  box-shadow: inset 3px 0 0 #2dd4bf;
}
.dot {
  width: 8px;
  height: 8px;
  border-radius: 999px;
  display: inline-block;
}
</style>
