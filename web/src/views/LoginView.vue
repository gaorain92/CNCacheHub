<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { Lock, User } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const router = useRouter()
const route = useRoute()

const username = ref('')
const password = ref('')
const submitting = ref(false)

onMounted(async () => {
  // 如果已经登录，直接跳走
  if (auth.isAuthenticated) {
    const redirect = (route.query.redirect as string) || '/dashboard'
    router.replace(redirect)
  }
})

async function onSubmit(): Promise<void> {
  if (!username.value || !password.value) {
    ElMessage.warning('请输入用户名和密码')
    return
  }
  submitting.value = true
  const ok = await auth.login(username.value, password.value)
  submitting.value = false
  if (ok) {
    ElMessage.success('登录成功')
    const redirect = (route.query.redirect as string) || '/dashboard'
    router.replace(redirect)
  } else {
    ElMessage.error(auth.errorMessage || '登录失败')
  }
}
</script>

<template>
  <div class="min-h-screen flex items-center justify-center px-4">
    <!-- 背景装饰：渐变光晕 -->
    <div class="absolute inset-0 overflow-hidden pointer-events-none">
      <div class="absolute -top-32 -left-32 w-96 h-96 rounded-full bg-mint/10 blur-3xl"></div>
      <div class="absolute -bottom-32 -right-32 w-96 h-96 rounded-full bg-violet/10 blur-3xl"></div>
    </div>

    <div class="relative w-full max-w-md">
      <!-- Logo + 标题 -->
      <div class="text-center mb-8">
        <div class="inline-flex h-16 w-16 rounded-2xl bg-gradient-to-br from-mint to-violet items-center justify-center shadow-glow mb-4">
          <span class="text-2xl font-bold text-ink">CN</span>
        </div>
        <h1 class="text-3xl font-semibold tracking-tight">CNCacheHub</h1>
        <p class="text-sm text-slate-400 mt-2">自托管下载加速中枢 · 控制台登录</p>
      </div>

      <!-- 登录卡片 -->
      <div class="rounded-2xl border border-white/[.08] bg-black/30 backdrop-blur p-6 shadow-glow">
        <h2 class="text-lg font-semibold mb-4 flex items-center gap-2">
          <el-icon :size="18" color="#94a3b8"><Lock /></el-icon>
          登录
        </h2>

        <el-form @submit.prevent="onSubmit" label-position="top">
          <el-form-item label="用户名">
            <el-input
              v-model="username"
              placeholder="admin"
              size="large"
              :prefix-icon="User"
              autocomplete="username"
              autofocus
            />
          </el-form-item>
          <el-form-item label="密码">
            <el-input
              v-model="password"
              type="password"
              placeholder="••••••••"
              size="large"
              :prefix-icon="Lock"
              autocomplete="current-password"
              show-password
              @keyup.enter="onSubmit"
            />
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
            登录
          </el-button>
        </el-form>
      </div>

      <p class="text-xs text-slate-500 text-center mt-6">
        首次启动？<router-link to="/init" class="text-mint hover:underline">创建管理员账号</router-link>
      </p>
    </div>
  </div>
</template>
