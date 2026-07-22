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
  SetUp,
} from '@element-plus/icons-vue'
import type { Component } from 'vue'

interface NavItem {
  name: RouteRecordName | null
  label: string
  path: string
  icon: Component
  badge?: string
}

const props = defineProps<{ embedded?: boolean }>()
const emit = defineEmits<{ navigate: [] }>()

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
  { name: 'settings', path: '/settings', label: '系统设置', icon: SetUp },
]

const activeName = computed(() => route.name)

function go(item: NavItem): void {
  router.push(item.path)
  emit('navigate')
}

const asideClass = computed(() =>
  props.embedded
    ? 'w-full h-full glass rounded-none p-5 flex flex-col'
    : 'fixed left-5 top-5 bottom-5 w-[17.5rem] glass rounded-[2rem] p-5 flex flex-col z-20'
)
</script>

<template>
  <aside :class="asideClass">
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

    <!-- Footer: model status -->
    <div class="mt-4">
      <div class="rounded-3xl border border-mint/20 bg-mint/10 p-4">
        <div class="flex items-center gap-2 text-sm font-medium text-mint">
          <span class="dot bg-mint animate-pulse" />
          单 VPS 自托管模式
        </div>
        <p class="mt-2 text-xs leading-5 text-slate-300">
          默认白名单代理（不开放任意 URL）。缓存上限与系统保底空间可在「系统设置」中调整。
        </p>
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
