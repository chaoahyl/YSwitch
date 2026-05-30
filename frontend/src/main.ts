import { createApp } from "vue";
import App from "./App.vue";
import { router } from "./router";
import "./style.css";

import { createPinia } from "pinia";
import piniaPluginPersistedstate from "pinia-plugin-persistedstate";
import { i18n } from "./i18n";
import { useLocaleStore } from "./stores/locale";

const app = createApp(App);

const pinia = createPinia();
pinia.use(piniaPluginPersistedstate);

app.use(pinia);
app.use(i18n);

// Sync persisted locale to i18n on startup
const localeStore = useLocaleStore();
localeStore.init();

app.use(router);
app.mount("#app");
