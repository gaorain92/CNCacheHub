<script setup lang="ts">
import { DataLine } from '@element-plus/icons-vue'
</script>

<template>
  <section class="soft rounded-[2rem] p-8">
    <div class="flex items-center gap-3">
      <div class="h-12 w-12 rounded-2xl bg-gradient-to-br from-mint to-violet flex items-center justify-center shadow-glow">
        <el-icon :size="22" color="#020617"><DataLine /></el-icon>
      </div>
      <div>
        <h2 class="text-2xl font-semibold">缓存管理</h2>
        <p class="text-sm text-slate-400">查看缓存占用、清理候选、定时清理与小容量 VPS 优化。</p>
      </div>
    </div>
    <div class="mt-6 rounded-3xl border border-white/[.08] bg-black/20 p-6 text-sm text-slate-300">
      Phase 0 占位 · 待 Phase 2 接入缓存条目可视化与清理任务。
    </div>
  </section>
</template>
