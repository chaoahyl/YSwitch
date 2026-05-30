<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";

const props = defineProps<{
  message?: string;
  error?: string;
}>();

const visibleMessage = ref("");
const visibleError = ref("");
let hideTimer: ReturnType<typeof setTimeout> | null = null;

const displayText = computed(() => visibleError.value || visibleMessage.value);

watch(
  () => props.error,
  (nextError) => {
    clearHideTimer();
    visibleError.value = nextError || "";
    if (nextError) {
      visibleMessage.value = "";
      hideTimer = setTimeout(() => {
        visibleError.value = "";
        hideTimer = null;
      }, 5000);
    }
  },
  { immediate: true },
);

watch(
  () => props.message,
  (nextMessage) => {
    if (props.error) {
      return;
    }
    clearHideTimer();
    visibleMessage.value = nextMessage || "";
    if (nextMessage) {
      hideTimer = setTimeout(() => {
        visibleMessage.value = "";
        hideTimer = null;
      }, 1000);
    }
  },
  { immediate: true },
);

onBeforeUnmount(() => {
  clearHideTimer();
});

function clearHideTimer() {
  if (hideTimer) {
    clearTimeout(hideTimer);
    hideTimer = null;
  }
}
</script>

<template>
  <transition
    enter-active-class="transition duration-200 ease-out"
    enter-from-class="opacity-0 translate-y-1"
    enter-to-class="opacity-100 translate-y-0"
    leave-active-class="transition duration-150 ease-in"
    leave-from-class="opacity-100"
    leave-to-class="opacity-0"
  >
    <div v-if="displayText" class="absolute bottom-5 left-1/2 z-20 w-[calc(100%-2rem)] max-w-sm -translate-x-1/2">
      <div
        class="animate-[toastIn_0.22s_cubic-bezier(0.4,0,0.2,1)] rounded-xl border border-[var(--app-border)] bg-[var(--app-panel)] px-5 py-3.5 text-center text-sm font-medium text-[var(--app-fg)] shadow-[var(--app-shadow)]"
        :class="{ 'border-transparent bg-[var(--red)] text-white': visibleError }"
      >
        {{ displayText }}
      </div>
    </div>
  </transition>
</template>
