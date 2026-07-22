<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { Quit, WindowMinimise, WindowToggleMaximise } from "../../wailsjs/runtime/runtime";
import { useThemeStore } from "../stores/theme";
import { useLocaleStore } from "../stores/locale";

defineProps<{ busy: boolean }>();

const theme = useThemeStore();
const locale = useLocaleStore();
const { t } = useI18n();

const confirmClose = ref(false);

const themeIcon = computed(() => (theme.darkMode ? "icon-[mdi--weather-sunny]" : "icon-[mdi--weather-night]"));
const themeLabel = computed(() => (theme.darkMode ? t('header.toggleLight') : t('header.toggleDark')));

function hasWailsRuntime() {
  return Boolean((window as unknown as { runtime?: unknown }).runtime);
}

function minimiseWindow() {
  if (hasWailsRuntime()) WindowMinimise();
}

function maximiseWindow() {
  if (hasWailsRuntime()) WindowToggleMaximise();
}

function doQuit() {
  confirmClose.value = false;
  if (hasWailsRuntime()) Quit();
}
</script>

<template>
  <header
    class="app-titlebar flex h-11 shrink-0 items-center justify-between gap-3 bg-[var(--app-panel)]/80 pl-3 text-[var(--app-fg)] shadow-[0_1px_0_var(--app-border)] sm:pl-5"
  >
    <div class="flex min-w-0 items-center gap-3">
      <div class="min-w-0">
        <h1 class="truncate text-sm font-semibold leading-tight">YSwitch</h1>
      </div>
    </div>

    <div class="flex h-full items-center justify-end">
      <button
        class="no-drag inline-flex h-8 min-w-9 items-center justify-center rounded-md bg-transparent px-2 text-xs font-semibold text-[var(--app-fg)] transition-colors duration-150 hover:bg-[var(--app-inner)]"
        :aria-label="t('header.langToggle')"
        @click="locale.toggle()"
      >
        {{ t('header.langToggle') }}
      </button>
      <button
        class="no-drag mr-2 inline-flex h-8 w-8 items-center justify-center rounded-md bg-transparent text-sm font-medium text-[var(--app-fg)] transition-colors duration-150 hover:bg-[var(--app-inner)]"
        :aria-label="themeLabel"
        @click="theme.toggle()"
      >
        <span class="block h-4 w-4 shrink-0" :class="themeIcon"></span>
      </button>
      <button
        class="no-drag inline-flex h-11 w-11 items-center justify-center bg-transparent text-sm font-medium text-[var(--app-fg)] transition-colors duration-150 hover:bg-[var(--app-inner)]"
        :aria-label="t('header.minimize')"
        @click="minimiseWindow"
      >
        <span class="block h-4 w-4 shrink-0 icon-[mdi--window-minimize]"></span>
      </button>
      <button
        class="no-drag inline-flex h-11 w-11 items-center justify-center bg-transparent text-sm font-medium text-[var(--app-fg)] transition-colors duration-150 hover:bg-[var(--app-inner)]"
        :aria-label="t('header.maximize')"
        @click="maximiseWindow"
      >
        <span class="block h-4 w-4 shrink-0 icon-[mdi--window-maximize]"></span>
      </button>
      <button
        class="no-drag inline-flex h-11 w-11 items-center justify-center bg-transparent text-sm font-medium text-[var(--app-fg)] transition-colors duration-150 hover:bg-[var(--red)] hover:text-white"
        :aria-label="t('header.close')"
        @click="confirmClose = true"
      >
        <span class="block h-4 w-4 shrink-0 icon-[mdi--close]"></span>
      </button>
    </div>
  </header>

  <Transition name="confirm-modal">
  <div
    v-if="confirmClose"
    class="no-drag fixed inset-0 z-50 flex items-center justify-center bg-black/35 backdrop-blur-[3px]"
    @click.self="confirmClose = false"
  >
    <div
      class="glass-card w-[min(260px,calc(100vw-2rem))] p-6 [transform:translateZ(0)]"
    >
      <p class="mb-1 text-sm font-semibold">{{ t('header.confirmCloseTitle') }}</p>
      <p class="mb-5 text-xs leading-relaxed text-[var(--app-muted)]">{{ t('header.confirmCloseBody') }}</p>
      <div class="flex justify-end gap-2">
        <button
          class="soft-button h-9 w-20 text-sm font-medium"
          @click="confirmClose = false"
        >
          {{ t('common.cancel') }}
        </button>
        <button
          class="inline-flex h-9 w-20 items-center justify-center rounded-full bg-[var(--red)] text-sm font-semibold text-white outline-none transition-opacity duration-150 hover:opacity-80"
          @click="doQuit"
        >
          {{ t('common.close') }}
        </button>
      </div>
    </div>
  </div>
  </Transition>
</template>

<style scoped>
.confirm-modal-enter-active,
.confirm-modal-leave-active {
  transition: opacity 0.22s ease;
}
.confirm-modal-enter-active > div {
  transition: opacity 0.22s ease, transform 0.28s cubic-bezier(0.16, 1, 0.3, 1);
}
.confirm-modal-leave-active > div {
  transition: opacity 0.18s ease, transform 0.18s ease;
}
.confirm-modal-enter-from,
.confirm-modal-leave-to {
  opacity: 0;
}
.confirm-modal-enter-from > div,
.confirm-modal-leave-to > div {
  opacity: 0;
  transform: translateY(10px);
}
</style>
