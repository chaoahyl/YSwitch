<script setup lang="ts">
import { computed, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { useI18n } from "vue-i18n";
import { Quit, WindowMinimise, WindowToggleMaximise } from "../../wailsjs/runtime/runtime";
import { useThemeStore } from "../stores/theme";
import { useLocaleStore } from "../stores/locale";

const props = defineProps<{ busy: boolean }>();

const route = useRoute();
const router = useRouter();
const theme = useThemeStore();
const locale = useLocaleStore();
const { t } = useI18n();

const confirmClose = ref(false);

const isClaudePlatform = computed(() => route.path.startsWith("/claude"));
const isCodexPlatform = computed(() => !isClaudePlatform.value);
const themeIcon = computed(() => (theme.darkMode ? "icon-[mdi--weather-sunny]" : "icon-[mdi--weather-night]"));
const themeLabel = computed(() => (theme.darkMode ? t('header.toggleLight') : t('header.toggleDark')));

function switchToCodex() {
  router.push({ name: "codex" });
}

function switchToClaude() {
  router.push({ name: "claude" });
}

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
    class="app-titlebar grid h-12 shrink-0 grid-cols-[1fr_auto_1fr] items-center gap-3 border-b border-[var(--app-border)] bg-[var(--app-panel)] px-3 text-[var(--app-fg)] sm:h-14 sm:px-5"
  >
    <div class="flex min-w-0 items-center gap-3">
      <div class="min-w-0">
        <h1 class="truncate text-sm font-semibold leading-tight">YSwitch</h1>
      </div>
    </div>

    <div
      class="no-drag flex items-center justify-center gap-1 rounded-full border border-[var(--app-border)] bg-[var(--app-inner)] p-1 shadow-[inset_0_1px_2px_rgba(0,0,0,0.04)]"
    >
      <button
        class="relative h-7 rounded-full border border-transparent px-4 text-xs font-semibold outline-none transition-[color,background,border-color,box-shadow,opacity] duration-150 focus-visible:ring-2 focus-visible:ring-[var(--app-fg)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--app-panel)] disabled:opacity-60"
        :class="
          isCodexPlatform
            ? 'border-[var(--app-fg)] bg-[var(--app-fg)] text-[var(--app-bg)] shadow-[0_2px_8px_rgba(0,0,0,0.18)] hover:not:disabled:text-[var(--app-bg)]'
            : 'bg-transparent text-[var(--app-muted)] hover:not:disabled:bg-[var(--app-hover)] hover:not:disabled:text-[var(--app-fg)]'
        "
        :aria-pressed="isCodexPlatform"
        :disabled="props.busy"
        @click="switchToCodex"
      >
        Codex
      </button>
      <button
        class="relative h-7 rounded-full border border-transparent px-4 text-xs font-semibold outline-none transition-[color,background,border-color,box-shadow,opacity] duration-150 focus-visible:ring-2 focus-visible:ring-[var(--app-fg)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--app-panel)] disabled:opacity-60"
        :class="
          isClaudePlatform
            ? 'border-[var(--app-fg)] bg-[var(--app-fg)] text-[var(--app-bg)] shadow-[0_2px_8px_rgba(0,0,0,0.18)] hover:not:disabled:text-[var(--app-bg)]'
            : 'bg-transparent text-[var(--app-muted)] hover:not:disabled:bg-[var(--app-hover)] hover:not:disabled:text-[var(--app-fg)]'
        "
        :aria-pressed="isClaudePlatform"
        :disabled="props.busy"
        @click="switchToClaude"
      >
        Claude
      </button>
    </div>

    <div class="flex items-center justify-end gap-1">
      <button
        class="no-drag inline-flex h-8 w-8 items-center justify-center rounded-xl border border-[var(--app-border)] bg-transparent text-xs font-semibold text-[var(--app-fg)] outline-none transition-[background,color,opacity] duration-150 hover:bg-[var(--app-hover)]"
        :aria-label="t('header.langToggle')"
        @click="locale.toggle()"
      >
        {{ t('header.langToggle') }}
      </button>
      <button
        class="no-drag inline-flex h-8 w-8 items-center justify-center rounded-xl border border-[var(--app-border)] bg-transparent text-sm font-medium text-[var(--app-fg)] outline-none transition-[background,color,opacity] duration-150 hover:bg-[var(--app-hover)]"
        :aria-label="themeLabel"
        @click="theme.toggle()"
      >
        <span class="block h-4 w-4 shrink-0" :class="themeIcon"></span>
      </button>
      <button
        class="no-drag inline-flex h-8 w-8 items-center justify-center rounded-xl border border-[var(--app-border)] bg-transparent text-sm font-medium text-[var(--app-fg)] outline-none transition-[background,color,opacity] duration-150 hover:bg-[var(--app-hover)]"
        :aria-label="t('header.minimize')"
        @click="minimiseWindow"
      >
        <span class="block h-4 w-4 shrink-0 icon-[mdi--window-minimize]"></span>
      </button>
      <button
        class="no-drag inline-flex h-8 w-8 items-center justify-center rounded-xl border border-[var(--app-border)] bg-transparent text-sm font-medium text-[var(--app-fg)] outline-none transition-[background,color,opacity] duration-150 hover:bg-[var(--app-hover)]"
        :aria-label="t('header.maximize')"
        @click="maximiseWindow"
      >
        <span class="block h-4 w-4 shrink-0 icon-[mdi--window-maximize]"></span>
      </button>
      <button
        class="no-drag inline-flex h-8 w-8 items-center justify-center rounded-xl border border-[var(--app-border)] bg-transparent text-sm font-medium text-[var(--app-fg)] outline-none transition-[background,color,opacity] duration-150 hover:border-transparent hover:bg-[var(--red)] hover:text-white"
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
      class="w-[min(260px,calc(100vw-2rem))] rounded-2xl border border-[var(--app-border)] bg-[var(--app-panel)] p-6 shadow-[var(--app-shadow)] [transform:translateZ(0)]"
    >
      <p class="mb-1 text-sm font-semibold">{{ t('header.confirmCloseTitle') }}</p>
      <p class="mb-5 text-xs leading-relaxed text-[var(--app-muted)]">{{ t('header.confirmCloseBody') }}</p>
      <div class="flex justify-end gap-2">
        <button
          class="inline-flex h-9 w-20 items-center justify-center rounded-xl border border-[var(--app-border)] bg-transparent text-sm font-medium text-[var(--app-fg)] outline-none transition-[background,color,opacity] duration-150 hover:bg-[var(--app-hover)]"
          @click="confirmClose = false"
        >
          {{ t('common.cancel') }}
        </button>
        <button
          class="inline-flex h-9 w-20 items-center justify-center rounded-xl border border-transparent bg-[var(--red)] text-sm font-semibold text-white outline-none transition-opacity duration-150 hover:opacity-80"
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
