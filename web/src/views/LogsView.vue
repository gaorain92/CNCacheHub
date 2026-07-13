<script setup lang="ts">
import { List } from '@element-plus/icons-vue'
</script>

<template>
  <section class="soft rounded-[2rem] p-8">
    <div class="flex items-center gap-3">
      <div class="h-12 w-12 rounded-2xl bg-gradient-to-br from-mint to-violet flex items-center justify-center shadow-glow">
        <el-icon :size="22" color="#020617"><List /></el-icon>
      </div>
      <div>
        <h2 class="text-2xl font-semibold">请求日志</h2>
        <p class="text-sm text-slate-400">结构化请求日志、HIT/MISS 分布与从日志发起的诊断。</p>
      </div>
    </div>
    <div class="mt-6 rounded-3xl border border-white/[.08] bg-black/20 p-6 text-sm text-slate-300">
      Phase 0 占位 · 待 Phase 1 接入基础请求日志（结构化、过滤、导出）。
    </div>
  </section>
</template>
