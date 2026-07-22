<script setup lang="ts">
import { ref } from 'vue'
import { Operation } from '@element-plus/icons-vue'
import Sidebar from '@/components/Sidebar.vue'
import TopBar from '@/components/TopBar.vue'

// 移动端抽屉：< lg 隐藏侧栏，顶部按钮点开后从左侧滑出
const drawerOpen = ref(false)
function closeDrawer(): void {
  drawerOpen.value = false
}
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

    <!-- Desktop sidebar (>= lg) -->
    <div class="hidden lg:block">
      <Sidebar />
    </div>

    <!-- Mobile drawer (< lg) -->
    <div
      v-if="drawerOpen"
      class="lg:hidden fixed inset-0 z-30 bg-black/50 backdrop-blur-sm"
      @click="closeDrawer"
    />
    <transition name="drawer">
      <div
        v-if="drawerOpen"
        class="lg:hidden fixed left-0 top-0 bottom-0 w-[18rem] z-40"
      >
        <Sidebar embedded @navigate="closeDrawer" />
      </div>
    </transition>

    <main class="relative w-full lg:ml-[19rem] lg:w-[calc(100%-19rem)] px-4 sm:px-6 py-5 min-h-screen">
      <!-- Mobile menu trigger -->
      <div class="lg:hidden mb-3 flex items-center gap-2">
        <button
          type="button"
          class="btn rounded-2xl bg-white/[.06] px-3 py-2 text-sm hover:bg-white/[.10] transition flex items-center gap-2"
          aria-label="打开侧栏"
          @click="drawerOpen = true"
        >
          <el-icon :size="16"><Operation /></el-icon>
          菜单
        </button>
      </div>

      <TopBar />
      <div class="space-y-5">
        <slot />
      </div>
    </main>
  </div>
</template>

<style scoped>
.drawer-enter-active,
.drawer-leave-active {
  transition: transform 0.22s ease;
}
.drawer-enter-from,
.drawer-leave-to {
  transform: translateX(-100%);
}
</style>
