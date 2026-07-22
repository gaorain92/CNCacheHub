<script setup lang="ts">
import { FirstAidKit } from '@element-plus/icons-vue'
</script>

<template>
  <section class="soft rounded-[2rem] p-8 space-y-6">
    <header class="flex items-center gap-3">
      <div class="h-12 w-12 rounded-2xl bg-gradient-to-br from-mint to-violet flex items-center justify-center shadow-glow">
        <el-icon :size="22" color="#020617"><FirstAidKit /></el-icon>
      </div>
      <div>
        <h2 class="text-2xl font-semibold">诊断中心</h2>
        <p class="text-sm text-slate-400">下载失败 / DNS 未生效 / 证书问题的一键排查。</p>
      </div>
    </header>

    <div class="rounded-3xl border border-white/[.08] bg-black/20 p-8 text-center space-y-3">
      <div class="inline-flex items-center gap-2 rounded-full bg-mint/10 text-mint text-xs px-3 py-1 border border-mint/20">
        P1 开发中
      </div>
      <h3 class="text-lg font-semibold text-slate-100">即将开放 · 诊断剧本</h3>
      <p class="text-sm text-slate-400 leading-relaxed max-w-xl mx-auto">
        计划支持三类剧本：Docker 拉取失败（401/404/拉取慢）、SteamCMD
        DNS 未生效（国内解析到 Akamai/HK 节点）、TLS / 反代配置错误（证书不匹配 / 502）。
        每类剧本会聚合最近日志 + 上游延迟 + 错误样本，给出可执行的修复建议。
      </p>
      <div class="text-xs text-slate-500">参考：PRD §9.7 / §19.2 P1</div>
    </div>
  </section>
</template>
