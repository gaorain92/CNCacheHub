<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowDown, Edit, Lightning, MagicStick, Search, SwitchButton, User } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import StatusDot from './StatusDot.vue'
import ChangePasswordDialog from './ChangePasswordDialog.vue'
import { useHealthStore } from '@/stores/health'
import { useAuthStore } from '@/stores/auth'

const route = useRoute()
const router = useRouter()

const health = useHealthStore()
const auth = useAuthStore()

const showChangePwd = ref(false)

const pageTitle = computed(() => (route.meta?.title as string | undefined) ?? 'CNCacheHub')

// 当前节点：用 window.location.host（不带端口），无 host 则降级到「默认节点」
const envLabel = computed(() => {
  if (typeof window === 'undefined') return '默认节点'
  const host = window.location.hostname || '默认节点'
  return `当前节点 · ${host}`
})

const dotStatus = computed(() => {
  // 还没开始第一次检查 → unknown（灰 + 脉冲）
  if (health.lastCheckedAt === 0) return 'unknown' as const
  // 检查过且 status=ok + 拿到数据 → ok（绿）
  if (health.status === 'ok' && health.backendConnected) return 'ok' as const
  // 明确知道 down → down（红）
  if (health.status === 'down' || health.errorMessage) return 'down' as const
  // 拿不到数据但也没明确错误 → unknown
  return 'unknown' as const
})

const connLabel = computed(() => {
  if (health.lastCheckedAt === 0) return '检测中…'
  if (health.status === 'ok' && health.backendConnected) return '后端已连接'
  if (health.status === 'down' || health.errorMessage) return '后端未连接'
  return '检测中…'
})

const connColorClass = computed(() => {
  if (connLabel.value === '后端已连接') return 'text-mint'
  if (connLabel.value === '后端未连接') return 'text-rose-300'
  return 'text-slate-400'
})

function goDiagnostics(): void {
  router.push('/diagnostics')
}

function goClients(): void {
  router.push('/clients')
}

async function handleLogout(): Promise<void> {
  try {
    await ElMessageBox.confirm('确定要登出当前账号吗？', '登出确认', {
      type: 'warning',
      confirmButtonText: '登出',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  await auth.logout()
  ElMessage.success('已登出')
  router.replace('/login')
}

function onUserCommand(c: string): void {
  if (c === 'logout') handleLogout()
  if (c === 'change-password') showChangePwd.value = true
}
</script>

<template>
  <header
    class="sticky top-5 z-10 mb-5 glass rounded-2xl px-5 py-3.5 flex items-center justify-between gap-4"
  >
    <div class="min-w-0">
      <div class="text-xs text-slate-400 flex items-center gap-2">
        <span>{{ envLabel }}</span>
        <span class="hidden sm:inline">·</span>
        <StatusDot :status="dotStatus" size="sm" :pulse="true" />
        <span :class="connColorClass">{{ connLabel }}</span>
      </div>
      <h1 class="mt-1 text-xl font-semibold tracking-tight truncate">
        {{ pageTitle }}
      </h1>
    </div>

    <div class="flex items-center gap-3 shrink-0">
      <div
        class="hidden xl:flex items-center gap-2 rounded-2xl bg-white/[.05] px-4 py-2 text-sm text-slate-300 min-w-[16rem]"
      >
        <el-icon :size="14" class="text-slate-500"><Search /></el-icon>
        <span class="text-slate-500">搜索镜像 / AppID / 日志 / IP</span>
      </div>
      <button
        type="button"
        class="btn rounded-2xl bg-white/[.06] px-4 py-2 text-sm hover:bg-white/[.10] transition flex items-center gap-2"
        @click="goDiagnostics"
      >
        <el-icon :size="14" color="#a3e635"><Lightning /></el-icon>
        快速诊断
      </button>
      <button
        type="button"
        class="btn rounded-2xl bg-gradient-to-r from-mint to-violet px-4 py-2 text-sm font-semibold text-ink shadow-glow transition flex items-center gap-2"
        @click="goClients"
      >
        <el-icon :size="14"><MagicStick /></el-icon>
        生成配置
      </button>

      <!-- 用户菜单 -->
      <el-dropdown trigger="click" @command="onUserCommand">
        <button
          type="button"
          class="btn rounded-2xl bg-white/[.06] px-3 py-2 text-sm hover:bg-white/[.10] transition flex items-center gap-2"
        >
          <div class="h-7 w-7 rounded-full bg-gradient-to-br from-mint to-violet flex items-center justify-center text-xs font-bold text-ink">
            <el-icon :size="14"><User /></el-icon>
          </div>
          <span class="text-slate-200">{{ auth.username }}</span>
          <el-icon :size="12" class="text-slate-500"><ArrowDown /></el-icon>
        </button>
        <template #dropdown>
          <el-dropdown-menu>
            <el-dropdown-item disabled>
              <span class="text-xs text-slate-500">
                {{ auth.user?.isAdmin ? '管理员' : '普通用户' }} · {{ auth.username }}
              </span>
            </el-dropdown-item>
            <el-dropdown-item divided command="change-password">
              <el-icon :size="14"><Edit /></el-icon>
              修改密码
            </el-dropdown-item>
            <el-dropdown-item command="logout">
              <el-icon :size="14"><SwitchButton /></el-icon>
              登出
            </el-dropdown-item>
          </el-dropdown-menu>
        </template>
      </el-dropdown>
    </div>
  </header>

  <ChangePasswordDialog v-model="showChangePwd" />
</template>
