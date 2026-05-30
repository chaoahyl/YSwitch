import { defineStore } from 'pinia'
import { ref } from 'vue'
import { WindowSetDarkTheme, WindowSetLightTheme } from '../../wailsjs/runtime/runtime'

export const useThemeStore = defineStore('theme', () => {
  const darkMode = ref(false)

  function toggle() {
    document.documentElement.classList.add('theme-transitioning')
    darkMode.value = !darkMode.value
    try {
      const hasRuntime = Boolean((window as unknown as { runtime?: unknown }).runtime)
      if (hasRuntime) {
        if (darkMode.value) {
          WindowSetDarkTheme()
        } else {
          WindowSetLightTheme()
        }
      }
    } catch {}
    setTimeout(() => document.documentElement.classList.remove('theme-transitioning'), 260)
  }

  return { darkMode, toggle }
}, {
  persist: true,
})
