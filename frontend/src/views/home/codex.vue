<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useRouter } from "vue-router";
import { useI18n } from "vue-i18n";
import AppHeader from "../../components/AppHeader.vue";
import ToastMessage from "../../components/ToastMessage.vue";
import { GetState, RefreshUsage } from "../../../wailsjs/go/main/App";
import type { main } from "../../../wailsjs/go/models";
import { useThemeStore } from "../../stores/theme";

type UsageSnapshot = main.UsageSnapshot;
type RateLimitWindow = main.RateLimitWindow;

const router = useRouter();
const theme = useThemeStore();
const { t, locale } = useI18n();

const emptyActive: main.AccountSummary = {
  label: "",
  fingerprint: "",
  updatedAt: "",
  authMode: "",
  accountId: "",
  plan: "",
  quota: "",
  entitlementSource: "",
};

const appState = ref<main.AppState | null>(null);
const busy = ref(false);
const usageBusy = ref(false);
const message = ref("");
const error = ref("");
const activeUsage = ref<UsageSnapshot | null>(null);
const loaded = ref(false);
const loading = ref(false);
const selectedProfile = ref("");
const hasActivated = ref(false);
const animatedValues = ref<Record<string, number>>({});
const rafHandles: Record<string, number> = {};

const profiles = computed(() => appState.value?.profiles ?? []);
const active = computed<main.AccountSummary>(() => appState.value?.active ?? emptyActive);
// 即使尚未导入，只要存在实时登录账号也展示账号卡片（卡片字段会回退到 active）。
const hasLiveAccount = computed(() => Boolean(active.value.fingerprint || active.value.label));
const activeProfile = computed(() => profiles.value.find((profile) => profile.id === selectedProfile.value) ?? null);
const showAccount = computed(() => hasActivated.value || hasLiveAccount.value);
const displayUsage = computed(() => activeUsage.value ?? appState.value?.usage ?? null);
const displayWindows = computed(() => normalizeWindows(displayUsage.value?.windows ?? []));
const displayAccountId = computed(
  () => activeProfile.value?.accountId || active.value.accountId || displayUsage.value?.accountId || "",
);
const displayUpdatedAt = computed(
  // 优先展示账号/凭证的更新时刻，而非额度请求时刻（后者每次刷新都会变成"现在"）。
  () => active.value.updatedAt || activeProfile.value?.updatedAt || displayUsage.value?.updatedAt || "",
);
const displayPlan = computed(
  () => displayUsage.value?.planType || activeProfile.value?.plan || active.value.plan || "",
);
const displayName = computed(
  () => activeProfile.value?.label || activeProfile.value?.name || active.value.label || t('common.noAccountDetected'),
);
const displayAuthMode = computed(() => activeProfile.value?.authMode || active.value.authMode || t('common.unknown'));
const displayFingerprint = computed(() => activeProfile.value?.fingerprint || active.value.fingerprint || "");

onMounted(() => {
  void ensureStateLoaded();
});

watch(
  displayWindows,
  (windows) => {
    for (const w of windows) {
      const key = `${w.limitName}-${w.windowDurationMins}`;
      const target = remaining(w.usedPercent);
      const current = animatedValues.value[key];
      if (current === undefined) {
        animatedValues.value[key] = 0;
        animateNum(key, 0, target);
      } else if (current !== target) {
        animateNum(key, current, target);
      }
    }
  },
  { immediate: true },
);

function animateNum(key: string, from: number, to: number) {
  if (rafHandles[key]) cancelAnimationFrame(rafHandles[key]);
  const start = performance.now();
  const dur = 800;
  function tick(now: number) {
    const t = Math.min((now - start) / dur, 1);
    animatedValues.value[key] = Math.round(from + (to - from) * (1 - Math.pow(1 - t, 3)));
    if (t < 1) {
      rafHandles[key] = requestAnimationFrame(tick);
    } else {
      animatedValues.value[key] = to;
      delete rafHandles[key];
    }
  }
  rafHandles[key] = requestAnimationFrame(tick);
}

function animVal(w: RateLimitWindow) {
  return animatedValues.value[`${w.limitName}-${w.windowDurationMins}`] ?? remaining(w.usedPercent);
}

async function ensureStateLoaded() {
  if (loaded.value || loading.value) {
    return;
  }
  loading.value = true;
  error.value = "";
  try {
    const state = await GetState();
    appState.value = state;
    activeUsage.value = state.usage;
    const saved = state.uiState;
    if (saved?.hasActivated && saved.selectedProfileId) {
      const exists = state.profiles.some((profile) => profile.id === saved.selectedProfileId);
      if (exists) {
        selectedProfile.value = saved.selectedProfileId;
        hasActivated.value = true;
      }
    }
  } catch (err) {
    error.value = normalizeError(err);
  } finally {
    loaded.value = true;
    loading.value = false;
  }
}

async function refreshUsage() {
  usageBusy.value = true;
  error.value = "";
  message.value = "";
  try {
    activeUsage.value = await RefreshUsage();
    message.value = t('codex.home.refreshed');
  } catch (err) {
    error.value = normalizeError(err);
  } finally {
    usageBusy.value = false;
  }
}

function normalizeWindows(windows: RateLimitWindow[]) {
  return [...windows].sort((a, b) => a.windowDurationMins - b.windowDurationMins);
}

function usageTitle(window: RateLimitWindow) {
  const name = window.limitName;
  const duration = formatDuration(window.windowDurationMins);
  if (name && /[一-鿿\d]/.test(name)) {
    return duration ? `${duration}-${name}` : name;
  }
  return duration || window.limitId || t('codex.home.usageFallback');
}

function formatDuration(minutes: number) {
  if (!minutes) return "";
  if (minutes >= 1440) return t('common.days', { n: Math.round(minutes / 1440) });
  if (minutes >= 60) return t('common.hours', { n: Math.round(minutes / 60) });
  return t('common.minutes', { n: minutes });
}

function dateLocaleTag() {
  return locale.value === 'zh' ? 'zh-CN' : 'en-US';
}

function formatResetTime(value: string) {
  if (!value) return t('common.unknown');
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(dateLocaleTag(), {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

function formatWindowReset(window: RateLimitWindow) {
  let date: Date;
  if (window.resetsAt) {
    date = new Date(window.resetsAt);
    if (Number.isNaN(date.getTime())) return window.resetsAt;
  } else if (window.windowDurationMins) {
    date = new Date(Date.now() + window.windowDurationMins * 60 * 1000);
  } else {
    return t('common.unknown');
  }
  return new Intl.DateTimeFormat(dateLocaleTag(), {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

function remaining(usedPercent: number) {
  return Math.max(0, Math.min(100, 100 - (usedPercent || 0)));
}

function pct(value: number) {
  return `${value.toFixed(0)}%`;
}

function shortValue(value: string, head = 6, tail = 4) {
  if (!value) return "NONE";
  if (value.length <= head + tail) return value;
  return `${value.slice(0, head)}...${value.slice(-tail)}`;
}

function fillColor(remain: number) {
  if (remain > 50) return "var(--green)";
  if (remain > 20) return "var(--orange)";
  return "var(--red)";
}

function usageStatus(usage?: UsageSnapshot | null) {
  if (!usage) return t('codex.home.usageNotLoaded');
  if (usage.status && usage.status !== "ok") return usage.status;
  if (!usage.windows?.length) return t('codex.home.usageNoData');
  return t('common.loaded');
}

function normalizeError(err: unknown) {
  if (!err) return t('common.actionFailed');
  if (typeof err === "string") return err;
  if (err instanceof Error) return err.message || t('common.actionFailed');
  return t('common.actionFailed');
}
</script>

<template>
  <main
    class="app-shell relative flex h-screen w-screen overflow-hidden bg-[var(--app-bg)] text-[var(--app-fg)]"
    :data-theme="theme.darkMode ? 'dark' : 'light'"
  >
    <div class="pointer-events-none absolute inset-0"></div>
    <section class="relative z-10 flex h-screen w-full flex-col">
      <AppHeader :busy="busy || usageBusy" />

      <!-- Skeleton loading screen -->
      <div v-if="!loaded" class="skel-wrap flex flex-col gap-2.5 px-3 pb-4 pt-3 sm:gap-3 sm:px-4">
        <div class="flex items-center gap-2 px-0.5 py-0.5">
          <div class="flex gap-1">
            <span class="skeleton h-1.5 w-1.5 rounded-full" style="animation-delay: 0ms; animation-duration: 1.2s"></span>
            <span class="skeleton h-1.5 w-1.5 rounded-full" style="animation-delay: 200ms; animation-duration: 1.2s"></span>
            <span class="skeleton h-1.5 w-1.5 rounded-full" style="animation-delay: 400ms; animation-duration: 1.2s"></span>
          </div>
          <span class="text-xs text-[var(--app-muted)]">{{ t('codex.home.loadingHint') }}</span>
        </div>
        <div class="rounded-xl border border-[var(--app-border)] bg-[var(--app-panel)] p-5 shadow-[var(--app-shadow)]">
          <div class="flex items-start justify-between gap-3">
            <div class="flex flex-1 flex-col gap-2.5">
              <div class="skeleton h-3 w-20 rounded-full"></div>
              <div class="skeleton h-5 w-40 rounded-lg"></div>
            </div>
            <div class="skeleton h-6 w-20 rounded-full"></div>
          </div>
          <div class="mt-4 grid grid-cols-2 gap-4 border-t border-[var(--app-border)] pt-4">
            <div v-for="n in 4" :key="n" class="flex flex-col gap-2">
              <div class="skeleton h-2.5 w-14 rounded-full"></div>
              <div class="skeleton h-4 w-24 rounded-md"></div>
            </div>
          </div>
        </div>
        <div class="rounded-xl border border-[var(--app-border)] bg-[var(--app-panel)] p-4 shadow-[var(--app-shadow)] sm:p-5">
          <div class="mb-4 flex items-center justify-between">
            <div class="skeleton h-4 w-16 rounded-md"></div>
            <div class="skeleton h-7 w-14 rounded-xl"></div>
          </div>
          <div class="grid gap-3 sm:grid-cols-2">
            <div v-for="n in 2" :key="n" class="rounded-xl border border-[var(--app-border)] bg-[var(--app-inner)] p-4">
              <div class="flex items-start justify-between gap-2">
                <div class="skeleton h-3 w-16 rounded-full"></div>
                <div class="skeleton h-8 w-12 rounded-lg"></div>
              </div>
              <div class="skeleton mt-3 h-1.5 w-full rounded-full"></div>
              <div class="mt-3 flex items-center justify-between">
                <div class="skeleton h-2.5 w-12 rounded-full"></div>
                <div class="skeleton h-2.5 w-20 rounded-full"></div>
              </div>
            </div>
          </div>
        </div>
        <div class="skeleton h-[72px] rounded-xl"></div>
      </div>

      <div
        v-else
        class="content-scroll flex min-h-0 flex-1 flex-col gap-2.5 overflow-y-auto px-3 pb-4 pt-3 sm:gap-3 sm:px-4"
      >
        <template v-if="!showAccount">
          <div class="glass-card p-8 text-[var(--app-fg)]">
            <div
              class="flex flex-col items-center justify-center gap-1.5 py-10 text-center [&_p]:text-sm [&_p]:font-semibold [&_span]:text-xs [&_span]:text-[var(--app-muted)]"
              style="padding-top: 0.5rem; padding-bottom: 0.5rem"
            >
              <p>{{ t('codex.home.noAccount') }}</p>
              <span>{{ t('codex.home.noAccountHint') }}</span>
              <button
                class="primary-button mt-5 h-10 px-6 text-sm font-semibold"
                @click="router.push({ name: 'codex-account' })"
              >
                {{ t('codex.home.goToManage') }}
              </button>
            </div>
          </div>
        </template>

        <template v-else>
          <div class="glass-card p-5">
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0">
                <p class="text-xs font-medium uppercase tracking-wide text-[var(--app-muted)]">{{ t('common.currentAccount') }}</p>
                <p class="mt-1.5 break-all text-xl font-semibold leading-snug">
                  {{ displayName }}
                </p>
              </div>
              <span
                v-if="displayPlan"
                class="shrink-0 rounded-full bg-[var(--app-fg)] px-3 py-1 text-xs font-bold tracking-wider text-[var(--app-bg)]"
              >
                {{ t('common.planLevel', { plan: displayPlan }) }}
              </span>
            </div>
            <div class="mt-4 grid grid-cols-2 gap-4 border-t border-[var(--app-border)] pt-4">
              <div class="flex flex-col gap-1">
                <span class="text-xs text-[var(--app-muted)]">{{ t('common.authMode') }}</span>
                <span class="font-mono text-sm font-semibold">{{
                  displayAuthMode
                }}</span>
              </div>
              <div class="flex flex-col gap-1">
                <span class="text-xs text-[var(--app-muted)]">{{ t('common.fingerprint') }}</span>
                <span class="font-mono text-sm font-semibold">{{
                  shortValue(displayFingerprint)
                }}</span>
              </div>
              <div class="flex flex-col gap-1">
                <span class="text-xs text-[var(--app-muted)]">{{ t('common.accountId') }}</span>
                <span class="font-mono text-sm font-semibold">{{ shortValue(displayAccountId) }}</span>
              </div>
              <div class="flex flex-col gap-1">
                <span class="text-xs text-[var(--app-muted)]">{{ t('common.updatedAt') }}</span>
                <span class="font-mono text-sm font-semibold">{{ formatResetTime(displayUpdatedAt) }}</span>
              </div>
            </div>
          </div>

          <section class="glass-card p-4 text-[var(--app-fg)] sm:p-5">
            <div class="mb-4 flex min-w-0 items-center justify-between gap-3 [&_h2]:text-base [&_h2]:font-semibold">
              <h2>{{ t('codex.home.usageSection') }}</h2>
              <button
                class="primary-button h-8 min-w-16 px-3 text-xs font-semibold"
                :disabled="usageBusy"
                @click="refreshUsage"
              >
                <template v-if="usageBusy">
                  <svg class="h-3 w-3 animate-spin" viewBox="0 0 24 24" fill="none">
                    <circle cx="12" cy="12" r="9" stroke="currentColor" stroke-width="3" opacity="0.25" />
                    <path fill="currentColor" opacity="0.8" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                  </svg>
                  {{ t('common.loading') }}
                </template>
                <template v-else>{{ t('common.refresh') }}</template>
              </button>
            </div>

            <TransitionGroup v-if="displayWindows.length" name="usage-card" tag="div" class="grid gap-3 sm:grid-cols-2">
              <article
                v-for="(window, index) in displayWindows"
                :key="`${window.limitName}-${window.windowDurationMins}`"
                :style="{ '--i': index }"
                class="soft-card p-4 transition-colors duration-150"
              >
                <div
                  class="flex items-start justify-between gap-2 [&_h3]:text-xs [&_h3]:font-medium [&_h3]:uppercase [&_h3]:tracking-wide [&_h3]:text-[var(--app-muted)]"
                >
                  <h3>{{ usageTitle(window) }}</h3>
                  <span
                    class="text-3xl font-bold leading-none tabular-nums"
                    :style="{ color: fillColor(animVal(window)) }"
                  >
                    {{ pct(animVal(window)) }}
                  </span>
                </div>
                <div class="mt-3 h-1.5 overflow-hidden rounded-full bg-[var(--app-border)]">
                  <div
                    class="h-full rounded-full transition-[width] duration-500 ease-[cubic-bezier(0.4,0,0.2,1)]"
                    :style="{
                      width: pct(animVal(window)),
                      background: fillColor(animVal(window)),
                    }"
                  ></div>
                </div>
                <div
                  class="mt-3 flex items-center justify-between [&_span]:text-xs [&_span]:text-[var(--app-muted)] [&_strong]:text-xs [&_strong]:font-semibold"
                >
                  <span>{{ t('codex.home.remaining') }}</span>
                  <strong>{{ t('common.resetAt', { time: formatWindowReset(window) }) }}</strong>
                </div>
              </article>
            </TransitionGroup>

            <div
              v-else
              class="flex flex-col items-center justify-center gap-1.5 py-10 text-center [&_p]:text-sm [&_p]:font-semibold [&_span]:text-xs [&_span]:text-[var(--app-muted)]"
            >
              <p>{{ usageStatus(displayUsage) }}</p>
              <span>{{ t('codex.home.clickRefreshHint') }}</span>
            </div>
          </section>
        </template>

        <div class="grid gap-3">
          <button class="glass-card flex items-center justify-between p-4 text-left outline-none sm:p-5" @click="router.push({ name: 'codex-account' })">
            <div>
              <p class="m-0 text-base font-semibold">{{ t('codex.home.manageBtn') }}</p>
              <p class="m-0 mt-0.5 text-xs text-[var(--app-muted)]">
                {{ profiles.length ? t('codex.home.savedCount', { n: profiles.length }) : t('codex.home.addOrSwitch') }}
              </p>
            </div>
            <span class="h-4 w-4 shrink-0 text-[var(--app-muted)] icon-[mdi--arrow-right]"></span>
          </button>
        </div>
      </div>

      <ToastMessage :message="message" :error="error" />
    </section>
  </main>
</template>
