<script setup lang="ts">
import Sidebar from '@/components/Sidebar.vue'
import TopBar from '@/components/TopBar.vue'
</script>

<template>
  <div class="relative min-h-screen grid-bg antialiased">
    <!-- Background glow blobs -->
    <div class="fixed inset-0 pointer-events-none overflow-hidden">
      <div
        class="absolute -top-28 left-1/3 h-96 w-96 rounded-full bg-mint/10 blur-3xl"
      />
      <div
        class="absolute top-40 -right-20 h-[30rem] w-[30rem] rounded-full bg-violet/10 blur-3xl"
      />
    </div>

    <Sidebar />

    <main class="relative ml-80 w-[calc(100%-20rem)] px-6 py-5 min-h-screen">
      <TopBar />
      <div class="space-y-5">
        <slot />
      </div>
    </main>
  </div>
</template>
