<script setup lang="ts">
import { HotWater } from '@element-plus/icons-vue'
</script>

<template>
  <section class="soft rounded-[2rem] p-8 space-y-6">
    <header class="flex items-center gap-3">
      <div class="h-12 w-12 rounded-2xl bg-gradient-to-br from-mint to-violet flex items-center justify-center shadow-glow">
        <el-icon :size="22" color="#020617"><HotWater /></el-icon>
      </div>
      <div>
        <h2 class="text-2xl font-semibold">预热任务</h2>
        <p class="text-sm text-slate-400">批量输入镜像名或 Steam AppID，提前写入缓存。</p>
      </div>
    </header>

    <div class="rounded-3xl border border-white/[.08] bg-black/20 p-8 text-center space-y-3">
      <div class="inline-flex items-center gap-2 rounded-full bg-mint/10 text-mint text-xs px-3 py-1 border border-mint/20">
        P1 开发中
      </div>
      <h3 class="text-lg font-semibold text-slate-100">即将开放 · 预热任务调度</h3>
      <p class="text-sm text-slate-400 leading-relaxed max-w-xl mx-auto">
        计划支持：批量粘贴镜像列表或 AppID → 后台并发拉取 → 实时进度展示 →
        失败重试与命中统计。当前以缓存管理 + LRU/容量清理为底座，预热跑通后会接进来。
      </p>
      <div class="text-xs text-slate-500">参考：PRD §9.5 / §19.2 P1</div>
    </div>
  </section>
</template>
