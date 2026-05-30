import { defineStore } from 'pinia'
import { ref } from 'vue'
import { i18n } from '../i18n'
import { SetLocale } from '../../wailsjs/go/main/App'

export type LocaleCode = 'zh' | 'en'

export const useLocaleStore = defineStore('locale', () => {
  const locale = ref<LocaleCode>('zh')

  function toggle() {
    locale.value = locale.value === 'zh' ? 'en' : 'zh'
    i18n.global.locale.value = locale.value
    SetLocale(locale.value).catch(() => {})
  }

  function init() {
    i18n.global.locale.value = locale.value
    SetLocale(locale.value).catch(() => {})
  }

  return { locale, toggle, init }
}, {
  persist: true,
})
