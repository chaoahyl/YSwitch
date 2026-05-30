import { createRouter, createWebHashHistory } from 'vue-router'
import CodexHome from '../views/home/codex.vue'
import ClaudeHome from '../views/home/claude.vue'
import CodexAccount from '../views/account/codex.vue'
import ClaudeAccount from '../views/account/claude.vue'

export const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', name: 'codex', component: CodexHome },
    { path: '/accounts', name: 'codex-account', component: CodexAccount },
    { path: '/claude', name: 'claude', component: ClaudeHome },
    { path: '/claude/accounts', name: 'claude-account', component: ClaudeAccount },
  ],
})
