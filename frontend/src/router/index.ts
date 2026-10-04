import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: () => import('@/views/LoginView.vue') },
    {
      path: '/',
      component: () => import('@/components/AppLayout.vue'),
      children: [
        { path: '', name: 'dashboard', component: () => import('@/views/DashboardView.vue') },
        { path: 'chat', name: 'chat', component: () => import('@/views/ChatView.vue') },
        { path: 'keys', name: 'keys', component: () => import('@/views/KeysView.vue') },
        { path: 'users', name: 'users', component: () => import('@/views/UsersView.vue'), meta: { admin: true } },
        { path: 'bots', name: 'bots', component: () => import('@/views/BotsView.vue') },
        { path: 'agents', name: 'agents', component: () => import('@/views/AgentsView.vue') },
        { path: 'tasks', name: 'tasks', component: () => import('@/views/TasksView.vue') },
      ],
    },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

// 登录与角色守卫：未登录跳 /login；非 admin 访问 admin 页回首页。
router.beforeEach((to) => {
  const auth = useAuthStore()
  if (to.meta.requiresAuth === false || to.name === 'login') {
    if (to.name === 'login' && auth.isAuthed) return { name: 'dashboard' }
    return true
  }
  if (!auth.isAuthed) {
    return { name: 'login', query: to.fullPath !== '/' ? { redirect: to.fullPath } : undefined }
  }
  if (to.meta.admin && !auth.isAdmin) {
    return { name: 'dashboard' }
  }
  return true
})

export default router
