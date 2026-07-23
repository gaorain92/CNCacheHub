import { defineStore } from 'pinia'
import { ref } from 'vue'
import { runDiagnostics } from '@/api/diagnostics'
import type { DiagFullReport } from '@/types/api'

export const useDiagnosticsStore = defineStore('diagnostics', () => {
  const report = ref<DiagFullReport | null>(null)
  const running = ref(false)
  const errorMessage = ref('')
  const lastRunAt = ref(0)

  async function run(): Promise<boolean> {
    running.value = true
    errorMessage.value = ''
    try {
      report.value = await runDiagnostics()
      lastRunAt.value = Date.now()
      return true
    } catch (e) {
      errorMessage.value = (e as Error).message
      return false
    } finally {
      running.value = false
    }
  }

  return { report, running, errorMessage, lastRunAt, run }
})
