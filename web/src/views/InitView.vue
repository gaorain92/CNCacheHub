<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { Lock, User } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const router = useRouter()

const username = ref('admin')
const password = ref('')
const confirm = ref('')
const submitting = ref(false)

onMounted(async () => {
  // 拉取 init 状态（已初始化则跳登录）
  await auth.bootstrap()
  if (auth.initialized) {
    ElMessage.info('系统已初始化完成，请登录')
    router.replace('/login')
  }
})

function strength(p: string): { label: string; score: number; color: string } {
  if (!p) return { label: '', score: 0, color: 'slate' }
  let score = 0
  if (p.length >= 8) score++
  if (p.length >= 12) score++
  if (/[A-Z]/.test(p) && /[a-z]/.test(p)) score++
  if (/\d/.test(p)) score++
  if (/[^A-Za-z0-9]/.test(p)) score++
  const labels = ['极弱', '弱', '一般', '良好', '强', '极强']
  const colors = ['rose', 'rose', 'amber', 'amber', 'mint', 'mint']
  return { label: labels[score], score, color: colors[score] }
}

const pwStrength = ref(strength(''))
const match = ref(true)

function onPasswordChange(): void {
  pwStrength.value = strength(password.value)
  match.value = !confirm.value || confirm.value === password.value
}

function onConfirmChange(): void {
  match.value = confirm.value === password.value
}

async function onSubmit(): Promise<void> {
  if (!username.value.trim()) {
    ElMessage.warning('请输入用户名')
    return
  }
  if (password.value.length < 8) {
    ElMessage.warning('密码至少 8 位')
    return
  }
  if (password.value !== confirm.value) {
    ElMessage.warning('两次输入的密码不一致')
    return
  }
  submitting.value = true
  const ok = await auth.initAdmin(username.value.trim(), password.value)
  submitting.value = false
  if (ok) {
    ElMessage.success('初始化完成，欢迎使用 CNCacheHub')
    router.replace('/dashboard')
  } else {
    ElMessage.error(auth.errorMessage || '初始化失败')
  }
}
</script>

<template>
  <div class="min-h-screen flex items-center justify-center px-4">
    <div class="absolute inset-0 overflow-hidden pointer-events-none">
      <div class="absolute -top-32 -left-32 w-96 h-96 rounded-full bg-violet/10 blur-3xl"></div>
      <div class="absolute -bottom-32 -right-32 w-96 h-96 rounded-full bg-mint/10 blur-3xl"></div>
    </div>

    <div class="relative w-full max-w-md">
      <div class="text-center mb-8">
        <div class="inline-flex h-16 w-16 rounded-2xl bg-gradient-to-br from-mint to-violet items-center justify-center shadow-glow mb-4">
          <span class="text-2xl font-bold text-ink">CN</span>
        </div>
        <h1 class="text-3xl font-semibold tracking-tight">欢迎使用 CNCacheHub</h1>
        <p class="text-sm text-slate-400 mt-2">首次启动 · 创建管理员账号</p>
      </div>

      <div class="rounded-2xl border border-white/[.08] bg-black/30 backdrop-blur p-6 shadow-glow">
        <h2 class="text-lg font-semibold mb-4 flex items-center gap-2">
          <el-icon :size="18" color="#94a3b8"><User /></el-icon>
          管理员设置
        </h2>

        <el-form @submit.prevent="onSubmit" label-position="top">
          <el-form-item label="管理员用户名">
            <el-input
              v-model="username"
              placeholder="admin"
              size="large"
              :prefix-icon="User"
              autocomplete="username"
              autofocus
            />
          </el-form-item>

          <el-form-item label="密码（至少 8 位）">
            <el-input
              v-model="password"
              type="password"
              placeholder="••••••••"
              size="large"
              :prefix-icon="Lock"
              autocomplete="new-password"
              show-password
              @input="onPasswordChange"
            />
            <div v-if="password" class="mt-2 flex items-center gap-2 text-xs">
              <span class="text-slate-500">强度：</span>
              <span :class="`text-${pwStrength.color}-400 font-medium`">{{ pwStrength.label }}</span>
              <div class="flex-1 h-1 bg-white/[.06] rounded-full overflow-hidden">
                <div
                  class="h-full transition-all"
                  :class="`bg-${pwStrength.color}-400`"
                  :style="{ width: `${(pwStrength.score / 5) * 100}%` }"
                ></div>
              </div>
            </div>
          </el-form-item>

          <el-form-item label="确认密码">
            <el-input
              v-model="confirm"
              type="password"
              placeholder="••••••••"
              size="large"
              :prefix-icon="Lock"
              autocomplete="new-password"
              show-password
              :class="!match ? 'is-error' : ''"
              @input="onConfirmChange"
            />
            <div v-if="confirm && !match" class="text-xs text-rose-400 mt-1">两次密码不一致</div>
          </el-form-item>

          <div v-if="auth.errorMessage" class="rounded-lg border border-rose-500/20 bg-rose-500/5 p-3 text-sm text-rose-300 mb-3">
            {{ auth.errorMessage }}
          </div>

          <el-button
            type="primary"
            size="large"
            class="w-full"
            :loading="submitting"
            @click="onSubmit"
          >
            创建并进入控制台
          </el-button>
        </el-form>

        <p class="text-xs text-slate-500 mt-4 leading-relaxed">
          💡 密码以 bcrypt 存储；session cookie 有效期 7 天；
          完整初始化向导（部署模式、缓存策略）将在 phase 1.2 接入。
        </p>
      </div>
    </div>
  </div>
</template>
