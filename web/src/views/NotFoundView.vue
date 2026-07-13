<script setup lang="ts">
import { useRouter } from 'vue-router'

const router = useRouter()

function goHome(): void {
  router.push('/dashboard')
}
</script>

<template>
  <section class="min-h-[60vh] grid place-items-center">
    <div class="glass rounded-[2rem] p-10 text-center max-w-md">
      <div class="text-7xl font-bold tracking-tight text-gradient bg-gradient-to-r from-mint to-violet bg-clip-text text-transparent">
        404
      </div>
      <h2 class="mt-4 text-2xl font-semibold">页面未找到</h2>
      <p class="mt-2 text-sm text-slate-400">
        你访问的路径不存在，或者还在 Phase 1+ 才能开放。
      </p>
      <button
        type="button"
        class="btn mt-6 rounded-2xl bg-gradient-to-r from-mint to-violet px-5 py-3 text-sm font-semibold text-ink shadow-glow transition"
        @click="goHome"
      >
        返回首页
      </button>
    </div>
  </section>
</template>
