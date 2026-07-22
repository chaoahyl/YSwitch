import { createRouter, createWebHashHistory } from 'vue-router'
import CodexHome from '../views/home/codex.vue'
import CodexAccount from '../views/account/codex.vue'

export const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', name: 'codex', component: CodexHome },
    { path: '/accounts', name: 'codex-account', component: CodexAccount },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})
