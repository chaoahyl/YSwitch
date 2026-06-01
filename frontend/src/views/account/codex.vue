<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useRouter } from "vue-router";
import { useI18n } from "vue-i18n";
import AppHeader from "../../components/AppHeader.vue";
import ToastMessage from "../../components/ToastMessage.vue";
import {
  ActivateProfile,
  GetState,
  QuickImportAccount,
  RefreshAllUsage,
  SaveUIState,
} from "../../../wailsjs/go/main/App";
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

const addAccountSteps = computed(() => [
  t("codex.account.step0"),
  t("codex.account.step1"),
  t("codex.account.step2"),
  t("codex.account.step3"),
  t("codex.account.step4"),
]);

const appState = ref<main.AppState | null>(null);
const busy = ref(false);
const usageBusy = ref(false);
const detectBusy = ref(false);
const message = ref("");
const error = ref("");
const loaded = ref(false);
const loading = ref(false);
const selectedProfile = ref("");
const confirmProfile = ref<main.Profile | null>(null);

const active = computed<main.AccountSummary>(() => appState.value?.active ?? emptyActive);
const profiles = computed(() => appState.value?.profiles ?? []);
const activeMissing = computed(() => !active.value.fingerprint);
const savedActiveProfile = computed(
  () => profiles.value.find((profile) => accountMatchesProfile(active.value, profile)) ?? null,
);

onMounted(() => {
  void ensureStateLoaded();
});

async function ensureStateLoaded() {
  if (loaded.value || loading.value) return;
  loading.value = true;
  error.value = "";
  try {
    const state = await GetState();
    appState.value = state;
    syncSelectedProfile(state);
  } catch (err) {
    error.value = normalizeError(err);
  } finally {
    loaded.value = true;
    loading.value = false;
  }
}

function syncSelectedProfile(state: main.AppState, preferredId = selectedProfile.value) {
  const nextId = [preferredId, state.uiState.selectedProfileId].find(
    (id) => id && state.profiles.some((profile) => profile.id === id),
  );
  selectedProfile.value = nextId || "";
}

async function detectCurrent() {
  detectBusy.value = true;
  error.value = "";
  message.value = "";
  try {
    const fresh = await GetState();
    appState.value = fresh;
    syncSelectedProfile(fresh);
    message.value = t("codex.account.detectSuccess");
  } catch (err) {
    error.value = normalizeError(err);
  } finally {
    detectBusy.value = false;
  }
}

async function quickImport() {
  if (activeMissing.value) {
    try {
      const fresh = await GetState();
      appState.value = fresh;
      syncSelectedProfile(fresh);
    } catch {
      // 忽略，继续执行到下方的错误处理
    }
    if (activeMissing.value) {
      error.value = t("codex.account.noCurrentAccount");
      return;
    }
  }
  if (savedActiveProfile.value) {
    selectedProfile.value = savedActiveProfile.value.id;
    await SaveUIState(savedActiveProfile.value.id);
    message.value = t("codex.account.alreadySaved");
    return;
  }
  await runAction(async () => {
    const state = await QuickImportAccount();
    appState.value = state;
    syncSelectedProfile(state);
    message.value = t("codex.account.saveSuccess");
  });
}

async function refreshAllProfiles() {
  usageBusy.value = true;
  error.value = "";
  message.value = "";
  try {
    const state = await RefreshAllUsage();
    appState.value = state;
    syncSelectedProfile(state);
    message.value = t("codex.account.refreshAllSuccess");
  } catch (err) {
    error.value = normalizeError(err);
  } finally {
    usageBusy.value = false;
  }
}

function confirmActivate(profile: main.Profile) {
  confirmProfile.value = profile;
}

async function doActivate() {
  if (!confirmProfile.value) return;
  const profile = confirmProfile.value;
  confirmProfile.value = null;
  await activateProfile(profile);
}

async function activateProfile(profile?: main.Profile) {
  const id = profile?.id ?? selectedProfile.value;
  if (!id) {
    error.value = t("codex.account.selectAccount");
    return;
  }
  await runAction(async () => {
    let state: main.AppState;
    try {
      state = await ActivateProfile(id);
    } catch (err) {
      if (normalizeError(err) === "already active") {
        selectedProfile.value = id;
        await SaveUIState(id);
        message.value = t("codex.account.alreadyActive");
        return;
      }
      throw err;
    }
    appState.value = state;
    selectedProfile.value = id;
    await SaveUIState(id);
    const matched = accountMatchesProfile(state.active, profile);
    const restarted = !state.restartStatus || state.restartStatus === "ok";
    if (matched && restarted) {
      message.value = t("codex.account.switchSuccess");
    } else if (matched) {
      error.value = t("codex.account.switchRestartFail", { status: state.restartStatus });
    } else {
      error.value = t("codex.account.switchMismatch");
    }
  });
}

function accountMatchesProfile(activeAccount: main.AccountSummary, profile?: main.Profile) {
  if (!profile) return false;
  if (profile.accountId && activeAccount.accountId) {
    return profile.accountId === activeAccount.accountId;
  }
  if (profile.fingerprint && activeAccount.fingerprint) {
    return profile.fingerprint === activeAccount.fingerprint;
  }
  return profile.name === activeAccount.label;
}

async function runAction(action: () => Promise<void>) {
  busy.value = true;
  error.value = "";
  message.value = "";
  try {
    await action();
  } catch (err) {
    error.value = normalizeError(err);
  } finally {
    busy.value = false;
  }
}

function normalizeWindows(windows: RateLimitWindow[]) {
  return [...windows].sort((a, b) => a.windowDurationMins - b.windowDurationMins);
}

function formatDuration(minutes: number) {
  if (!minutes) return "";
  if (minutes >= 1440) return t("common.days", { n: Math.round(minutes / 1440) });
  if (minutes >= 60) return t("common.hours", { n: Math.round(minutes / 60) });
  return t("common.minutes", { n: minutes });
}

function usageTitle(window: RateLimitWindow) {
  const name = window.limitName;
  if (name && /[一-鿿\d]/.test(name)) return name;
  return formatDuration(window.windowDurationMins) || window.limitId || t("codex.home.usageFallback");
}

function remaining(usedPercent: number) {
  return Math.max(0, Math.min(100, 100 - (usedPercent || 0)));
}

function pct(value: number) {
  return `${value.toFixed(0)}%`;
}

function usageStatus(usage?: UsageSnapshot | null) {
  if (!usage) return t("codex.account.usageNotLoaded");
  if (usage.status && usage.status !== "ok") return usage.status;
  if (!usage.windows?.length) return t("codex.account.usageNoData");
  return t("common.loaded");
}

function dateLocaleTag() {
  return locale.value === "zh" ? "zh-CN" : "en-US";
}

function shortResetTime(window: RateLimitWindow) {
  let date: Date;
  if (window.resetsAt) {
    date = new Date(window.resetsAt);
    if (Number.isNaN(date.getTime())) return "";
  } else if (window.windowDurationMins) {
    date = new Date(Date.now() + window.windowDurationMins * 60 * 1000);
  } else {
    return "";
  }
  if ((window.windowDurationMins ?? 0) >= 1440) {
    return new Intl.DateTimeFormat(dateLocaleTag(), { month: "short", day: "numeric" }).format(date);
  }
  const hh = String(date.getHours()).padStart(2, "0");
  const mi = String(date.getMinutes()).padStart(2, "0");
  return `${hh}:${mi}`;
}

function profileUsageSummary(profile: main.Profile) {
  const windows = normalizeWindows(profile.usage?.windows ?? []);
  if (windows.length) {
    return windows
      .map((w) => {
        const reset = shortResetTime(w);
        return `${usageTitle(w)} ${pct(remaining(w.usedPercent))}${reset ? ` · ${reset}` : ""}`;
      })
      .join("  |  ");
  }
  // 始终展示该 profile 自身 usage 的真实状态，不因它不是当前账号就替换，也不借用当前账号的额度。
  // 真实错误状态（如"额度接口返回 429""认证已过期"）优先于存档的 quota 占位值展示。
  if (profile.usage?.status && profile.usage.status !== "ok") {
    return usageStatus(profile.usage);
  }
  if (profile.quota) return profile.quota;
  return usageStatus(profile.usage);
}

function profileUsageWindows(profile: main.Profile): string[] {
  const windows = normalizeWindows(profile.usage?.windows ?? []);
  if (!windows.length) return [];
  const items = windows.map((w) => {
    const reset = shortResetTime(w);
    return `${usageTitle(w)} ${pct(remaining(w.usedPercent))}${reset ? ` · ${reset}` : ""}`;
  });
  if (items.length === 1) {
    const isLong = (windows[0].windowDurationMins ?? 0) >= 1440;
    return isLong ? ["---", items[0]] : [items[0], "---"];
  }
  return items;
}

function profilePlanLabel(profile: main.Profile) {
  return profile.usage?.planType || profile.plan || t("codex.account.unknownPlan");
}

function normalizeError(err: unknown) {
  if (!err) return t("common.actionFailed");
  if (typeof err === "string") return err;
  if (err instanceof Error) return err.message || t("common.actionFailed");
  return t("common.actionFailed");
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
            <span
              class="skeleton h-1.5 w-1.5 rounded-full"
              style="animation-delay: 0ms; animation-duration: 1.2s"
            ></span>
            <span
              class="skeleton h-1.5 w-1.5 rounded-full"
              style="animation-delay: 200ms; animation-duration: 1.2s"
            ></span>
            <span
              class="skeleton h-1.5 w-1.5 rounded-full"
              style="animation-delay: 400ms; animation-duration: 1.2s"
            ></span>
          </div>
          <span class="text-xs text-[var(--app-muted)]">{{ t("codex.account.loadingHint") }}</span>
        </div>
        <div class="flex items-center gap-3">
          <div class="skeleton h-8 w-8 rounded-xl"></div>
          <div class="skeleton h-4 w-20 rounded-md"></div>
        </div>
        <div
          class="rounded-xl border border-[var(--app-border)] bg-[var(--app-panel)] p-4 shadow-[var(--app-shadow)] sm:p-5"
        >
          <div class="mb-4 flex items-center justify-between">
            <div class="skeleton h-4 w-16 rounded-md"></div>
            <div class="skeleton h-7 w-20 rounded-xl"></div>
          </div>
          <div class="skeleton mb-3 h-3 w-full rounded-full"></div>
          <div class="skeleton mb-3 h-3 w-4/5 rounded-full"></div>
          <div class="skeleton mb-3 h-[52px] w-full rounded-xl"></div>
          <div class="skeleton h-10 w-32 rounded-xl"></div>
        </div>
        <div
          class="rounded-xl border border-[var(--app-border)] bg-[var(--app-panel)] p-4 shadow-[var(--app-shadow)] sm:p-5"
        >
          <div class="mb-4 skeleton h-4 w-20 rounded-md"></div>
          <div class="space-y-3">
            <div v-for="n in 2" :key="n" class="flex items-center gap-3">
              <div class="skeleton h-8 w-8 shrink-0 rounded-full"></div>
              <div class="skeleton h-10 flex-1 rounded-xl"></div>
            </div>
          </div>
        </div>
        <div
          class="rounded-xl border border-[var(--app-border)] bg-[var(--app-panel)] p-4 shadow-[var(--app-shadow)] sm:p-5"
        >
          <div class="mb-4 flex items-center justify-between">
            <div class="skeleton h-4 w-20 rounded-md"></div>
            <div class="flex items-center gap-2">
              <div class="skeleton h-7 w-20 rounded-xl"></div>
              <div class="skeleton h-5 w-8 rounded-full"></div>
            </div>
          </div>
          <div class="space-y-1.5">
            <div v-for="n in 2" :key="n" class="skeleton h-[46px] rounded-xl"></div>
          </div>
        </div>
      </div>

      <div
        v-else
        class="content-scroll flex min-h-0 flex-1 flex-col gap-2.5 overflow-y-auto px-3 pb-4 pt-3 sm:gap-3 sm:px-4"
      >
        <div class="flex items-center justify-between gap-3">
          <div class="flex items-center gap-3">
            <button
              class="inline-flex h-8 w-8 items-center justify-center rounded-xl border border-[var(--app-border)] bg-transparent text-[var(--app-fg)] outline-none transition-colors duration-150 hover:bg-[var(--app-hover)]"
              :disabled="busy || usageBusy"
              @click="router.push({ name: 'codex' })"
            >
              <span class="block h-4 w-4 shrink-0 icon-[mdi--arrow-left]"></span>
            </button>
            <h2 class="text-base font-semibold">{{ t("codex.account.pageTitle") }}</h2>
          </div>
        </div>

        <section
          class="rounded-xl border border-[var(--app-border)] bg-[var(--app-panel)] p-4 text-[var(--app-fg)] shadow-[var(--app-shadow)] sm:p-5"
        >
          <div class="mb-4 flex min-w-0 items-center justify-between gap-3 [&_h2]:text-base [&_h2]:font-semibold">
            <h2>{{ t("codex.account.currentSection") }}</h2>
            <button
              class="no-drag inline-flex h-7 items-center justify-center gap-1 rounded-xl border border-[var(--app-border)] bg-transparent px-2.5 text-xs font-medium text-[var(--app-fg)] outline-none transition-[background,color,opacity] duration-150 hover:bg-[var(--app-hover)]"
              :disabled="detectBusy || busy"
              @click="detectCurrent"
            >
              <svg v-if="detectBusy" class="h-3.5 w-3.5 animate-spin" viewBox="0 0 24 24" fill="none">
                <circle cx="12" cy="12" r="9" stroke="currentColor" stroke-width="3" opacity="0.25" />
                <path fill="currentColor" opacity="0.8" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
              </svg>
              <span v-else class="block h-3.5 w-3.5 shrink-0 icon-[mdi--refresh]"></span>
              <span>{{ detectBusy ? t("common.loading") : t("codex.account.detectBtn") }}</span>
            </button>
          </div>

          <p class="mb-3 text-xs leading-relaxed text-[var(--app-muted)]">{{ t("codex.account.fileHint") }}</p>

          <div
            class="mb-3 rounded-xl border bg-[var(--app-inner)] p-3 transition-colors duration-200"
            :class="activeMissing ? 'border-[var(--red)] bg-[rgb(255_59_48_/_0.05)]' : 'border-[var(--app-border)]'"
          >
            <p class="m-0 text-sm font-semibold">{{ active.label || t("codex.account.noAccount") }}</p>
            <p class="mt-2 text-xs leading-relaxed text-[var(--app-muted)]">{{ t("codex.account.authFileLine") }}</p>
          </div>

          <div class="flex justify-start">
            <button
              class="inline-flex h-10 items-center justify-center gap-1.5 rounded-xl border border-transparent bg-[var(--app-fg)] px-6 text-sm font-semibold text-[var(--app-bg)] outline-none transition-[opacity,background,color] duration-150 hover:opacity-80 disabled:opacity-40"
              :disabled="busy || activeMissing"
              @click="quickImport"
            >
              <template v-if="busy">
                <svg class="h-3.5 w-3.5 animate-spin" viewBox="0 0 24 24" fill="none">
                  <circle cx="12" cy="12" r="9" stroke="currentColor" stroke-width="3" opacity="0.25" />
                  <path fill="currentColor" opacity="0.8" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                </svg>
                {{ t("common.saving") }}
              </template>
              <template v-else>{{ t("codex.account.saveAccount") }}</template>
            </button>
          </div>
        </section>

        <section
          class="rounded-xl border border-[var(--app-border)] bg-[var(--app-panel)] p-4 text-[var(--app-fg)] shadow-[var(--app-shadow)] sm:p-5"
        >
          <div class="mb-4 flex min-w-0 items-center justify-between gap-3 [&_h2]:text-base [&_h2]:font-semibold">
            <h2>{{ t("codex.account.stepsTitle") }}</h2>
          </div>

          <p class="mb-4 text-xs leading-relaxed text-[var(--app-muted)]">{{ t("codex.account.stepsNote") }}</p>

          <ol class="m-0 list-none space-y-3 p-0">
            <li v-for="(step, index) in addAccountSteps" :key="step" class="relative flex items-center gap-3">
              <span
                class="relative z-10 inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-[var(--app-fg)] text-xs font-semibold text-[var(--app-bg)]"
                >{{ index + 1 }}</span
              >
              <span
                v-if="index < addAccountSteps.length - 1"
                class="absolute bottom-[-14px] left-[15px] top-[38px] w-px bg-[var(--app-border)]"
              ></span>
              <div
                class="min-w-0 flex-1 rounded-xl border border-[var(--app-border)] bg-[var(--app-inner)] px-4 py-3 text-sm leading-relaxed text-[var(--app-fg)]"
              >
                {{ step }}
              </div>
            </li>
          </ol>
        </section>

        <section
          class="rounded-xl border border-[var(--app-border)] bg-[var(--app-panel)] p-4 text-[var(--app-fg)] shadow-[var(--app-shadow)] sm:p-5"
        >
          <div class="mb-4 flex min-w-0 items-center justify-between gap-3 [&_h2]:text-base [&_h2]:font-semibold">
            <h2>{{ t("codex.account.savedSection") }}</h2>
            <div class="flex items-center gap-2">
              <button
                class="no-drag inline-flex h-7 items-center justify-center gap-1 rounded-xl border border-[var(--app-border)] bg-transparent px-2.5 text-xs font-medium text-[var(--app-fg)] outline-none transition-[background,color,opacity] duration-150 hover:bg-[var(--app-hover)]"
                :disabled="busy || usageBusy"
                @click="refreshAllProfiles"
              >
                <svg v-if="usageBusy" class="h-3.5 w-3.5 animate-spin" viewBox="0 0 24 24" fill="none">
                  <circle cx="12" cy="12" r="9" stroke="currentColor" stroke-width="3" opacity="0.25" />
                  <path fill="currentColor" opacity="0.8" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                </svg>
                <span class="block h-4 w-4 shrink-0 icon-[mdi--refresh]" v-else></span>
                <span>{{ usageBusy ? t("common.refreshing") : t("codex.account.refreshUsage") }}</span>
              </button>
              <span
                class="rounded-full border border-[var(--app-border)] px-2 py-0.5 text-xs font-medium text-[var(--app-muted)]"
                >{{ t("codex.account.countSuffix", { n: profiles.length }) }}</span
              >
            </div>
          </div>

          <div
            v-if="!profiles.length"
            class="flex flex-col items-center justify-center gap-1.5 py-10 text-center [&_p]:text-sm [&_p]:font-semibold [&_span]:text-xs [&_span]:text-[var(--app-muted)]"
          >
            <p>{{ t("codex.account.noSaved") }}</p>
            <span>{{ t("codex.account.noSavedHint") }}</span>
          </div>

          <div v-else class="space-y-1.5">
            <article
              v-for="profile in profiles"
              :key="profile.id"
              class="cursor-pointer rounded-xl border px-3 py-2 transition-colors duration-150"
              :class="
                selectedProfile === profile.id
                  ? 'border-[var(--app-fg)] bg-[var(--accent-soft)]'
                  : 'border-[var(--app-border)] bg-[var(--app-inner)] hover:bg-[var(--app-hover)]'
              "
              @click="confirmActivate(profile)"
            >
              <div class="flex min-w-0 items-center justify-between gap-2">
                <div class="flex min-w-0 flex-1 items-center gap-2">
                  <h3 class="truncate text-sm font-semibold">{{ profile.label || profile.name }}</h3>
                  <span
                    v-if="selectedProfile === profile.id"
                    class="flex h-4 w-4 shrink-0 items-center justify-center rounded-full bg-[var(--app-fg)] text-[9px] font-bold leading-none text-[var(--app-bg)]"
                    >✓</span
                  >
                </div>
                <div class="flex shrink-0 items-center gap-2">
                  <span
                    class="rounded-full border border-[var(--app-border)] px-2 py-0.5 text-xs font-medium text-[var(--app-fg)]"
                    >{{ profilePlanLabel(profile) }}</span
                  >
                  <button
                    class="inline-flex h-7 min-w-14 items-center justify-center rounded-xl border border-transparent bg-[var(--app-fg)] px-3 text-xs font-semibold text-[var(--app-bg)] outline-none transition-[opacity,background,color] duration-150 hover:opacity-80"
                    :disabled="busy || usageBusy"
                    @click.stop="confirmActivate(profile)"
                  >
                    {{ t("codex.account.switchBtn") }}
                  </button>
                </div>
              </div>
              <div v-if="profileUsageWindows(profile).length" class="mt-1.5 flex text-xs text-[var(--app-muted)]">
                <span v-for="(item, i) in profileUsageWindows(profile)" :key="i" class="min-w-0 flex-1 truncate">{{
                  item
                }}</span>
              </div>
              <p v-else class="mt-1.5 truncate text-xs text-[var(--app-muted)]">{{ profileUsageSummary(profile) }}</p>
            </article>
          </div>
        </section>
      </div>

      <ToastMessage :message="message" :error="error" />

      <Transition name="confirm-modal">
        <div
          v-if="confirmProfile"
          class="fixed inset-0 z-50 flex items-center justify-center bg-black/35 backdrop-blur-[3px]"
          @click.self="confirmProfile = null"
        >
          <div
            class="w-[min(300px,calc(100vw-2rem))] rounded-2xl border border-[var(--app-border)] bg-[var(--app-panel)] p-6 shadow-[var(--app-shadow)]"
          >
            <p class="mb-1 text-sm font-semibold">{{ t("codex.account.confirmSwitchTitle") }}</p>
            <p class="mb-5 text-xs leading-relaxed text-[var(--app-muted)]">
              {{ t("codex.account.confirmSwitchBody", { name: confirmProfile.label || confirmProfile.name }) }}
            </p>
            <div class="flex justify-end gap-2 [&_button]:h-[38px] [&_button]:w-[92px] [&_button]:px-0">
              <button
                class="inline-flex h-10 items-center justify-center rounded-xl border border-[var(--app-border)] bg-transparent px-6 text-sm font-medium text-[var(--app-fg)] outline-none transition-[background,color,opacity] duration-150 hover:bg-[var(--app-hover)]"
                @click="confirmProfile = null"
              >
                {{ t("common.cancel") }}
              </button>
              <button
                class="inline-flex h-10 items-center justify-center gap-1.5 rounded-xl border border-transparent bg-[var(--app-fg)] px-6 text-sm font-semibold text-[var(--app-bg)] outline-none transition-[opacity,background,color] duration-150 hover:opacity-80"
                :disabled="busy"
                @click="doActivate"
              >
                <template v-if="busy">
                  <svg class="h-3.5 w-3.5 animate-spin" viewBox="0 0 24 24" fill="none">
                    <circle cx="12" cy="12" r="9" stroke="currentColor" stroke-width="3" opacity="0.25" />
                    <path fill="currentColor" opacity="0.8" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                  </svg>
                  {{ t("common.switching") }}
                </template>
                <template v-else>{{ t("codex.account.confirmSwitch") }}</template>
              </button>
            </div>
          </div>
        </div>
      </Transition>
    </section>
  </main>
</template>

<style scoped>
.confirm-modal-enter-active,
.confirm-modal-leave-active {
  transition: opacity 0.22s ease;
}
.confirm-modal-enter-active > div {
  transition:
    opacity 0.22s ease,
    transform 0.28s cubic-bezier(0.16, 1, 0.3, 1);
}
.confirm-modal-leave-active > div {
  transition:
    opacity 0.18s ease,
    transform 0.18s ease;
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
