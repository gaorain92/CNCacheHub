<script setup lang="ts">
import { Goods } from '@element-plus/icons-vue'
</script>

<template>
  <section class="soft rounded-[2rem] p-8 space-y-6">
    <header class="flex items-center gap-3">
      <div class="h-12 w-12 rounded-2xl bg-gradient-to-br from-mint to-violet flex items-center justify-center shadow-glow">
        <el-icon :size="22" color="#020617"><Goods /></el-icon>
      </div>
      <div>
        <h2 class="text-2xl font-semibold">SteamCMD 加速</h2>
        <p class="text-sm text-slate-400">Steam 内容缓存、AppID 命中率与 DNS 转发配置。</p>
      </div>
    </header>

    <div class="rounded-3xl border border-white/[.08] bg-black/20 p-8 text-center space-y-3">
      <div class="inline-flex items-center gap-2 rounded-full bg-mint/10 text-mint text-xs px-3 py-1 border border-mint/20">
        P1 开发中
      </div>
      <h3 class="text-lg font-semibold text-slate-100">即将开放 · SteamCMD 缓存</h3>
      <p class="text-sm text-slate-400 leading-relaxed max-w-xl mx-auto">
        计划支持：DNS 启动器（cname.liiiy.cn 风格自建权威解析，把
        <code class="text-mint">steamcontent.com</code> /
        <code class="text-mint">cm2-ord1.cm.steampowered.com</code>
        等指到本机）；按 AppID 缓存 CS2 / Palworld / 幻兽帕鲁等专用服内容；命中 / 大小
        / 速度统计。
      </p>
      <div class="text-xs text-slate-500">参考：PRD §9.3 / §19.2 P1</div>
    </div>
  </section>
</template>
