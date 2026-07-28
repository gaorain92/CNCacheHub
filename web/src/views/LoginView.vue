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
  <div class="min-h-screen flex items-center justify-center px-4 bg-ink">
    <!-- 背景装饰 -->
    <div class="fixed inset-0 pointer-events-none overflow-hidden">
      <div class="absolute -top-40 left-1/4 w-[32rem] h-[32rem] rounded-full bg-mint/[.07] blur-[120px]" />
      <div class="absolute -bottom-40 right-1/4 w-[32rem] h-[32rem] rounded-full bg-violet/[.07] blur-[120px]" />
    </div>

    <div class="relative w-full max-w-sm">
      <!-- Logo -->
      <div class="text-center mb-8">
        <div class="inline-flex h-14 w-14 rounded-2xl bg-gradient-to-br from-mint to-teal-500 items-center justify-center mb-4 shadow-lg shadow-mint/20">
          <span class="text-xl font-bold text-ink tracking-tight">CN</span>
        </div>
        <h1 class="text-2xl font-semibold text-slate-100 tracking-tight">CNCacheHub</h1>
        <p class="text-sm text-slate-500 mt-1">自托管下载加速中枢</p>
      </div>

      <!-- 登录表单 -->
      <div class="rounded-xl border border-white/[.06] bg-panel p-6">
        <el-form @submit.prevent="onSubmit" label-position="top">
          <el-form-item label="用户名">
            <el-input
              v-model="username"
              placeholder="root"
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
              placeholder="输入密码"
              size="large"
              :prefix-icon="Lock"
              autocomplete="current-password"
              show-password
              @keyup.enter="onSubmit"
            />
          </el-form-item>

          <div
            v-if="auth.errorMessage"
            class="rounded-lg border border-rose-500/20 bg-rose-500/5 p-3 text-sm text-rose-300 mb-4"
          >
            {{ auth.errorMessage }}
          </div>

          <el-button
            type="primary"
            size="large"
            class="w-full !h-10 !text-sm !font-semibold"
            :loading="submitting"
            @click="onSubmit"
          >
            登录
          </el-button>
        </el-form>
      </div>

      <p class="text-xs text-slate-600 text-center mt-5">
        首次启动？
        <router-link to="/init" class="text-mint/80 hover:text-mint transition">创建管理员账号</router-link>
      </p>
    </div>
  </div>
</template>
