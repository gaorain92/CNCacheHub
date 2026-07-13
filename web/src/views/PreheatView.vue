<script setup lang="ts">
import { HotWater } from '@element-plus/icons-vue'
</script>

<template>
  <section class="soft rounded-[2rem] p-8">
    <div class="flex items-center gap-3">
      <div class="h-12 w-12 rounded-2xl bg-gradient-to-br from-mint to-violet flex items-center justify-center shadow-glow">
        <el-icon :size="22" color="#020617"><HotWater /></el-icon>
      </div>
      <div>
        <h2 class="text-2xl font-semibold">预热任务</h2>
        <p class="text-sm text-slate-400">批量输入镜像名或 Steam AppID，提前写入缓存。</p>
      </div>
    </div>
    <div class="mt-6 rounded-3xl border border-white/[.08] bg-black/20 p-6 text-sm text-slate-300">
      Phase 0 占位 · 待 Phase 2 接入预热任务调度与进度展示。
    </div>
  </section>
</template>
