import { createRouter, createWebHistory } from 'vue-router'
import { getStoredToken } from '../lib/storage'
import LoginView from '../views/LoginView.vue'

const router = createRouter({
  history: createWebHistory(),
  scrollBehavior: () => ({ top: 0 }),
  routes: [
    { path: '/', redirect: '/projects' },
    {
      path: '/login',
      name: 'login',
      component: LoginView,
      meta: { title: 'Sign in' },
    },
    {
      path: '/projects',
      name: 'projects',
      component: () => import('../views/ProjectListView.vue'),
      meta: { requiresAuth: true, title: 'Projects' },
    },
    {
      path: '/search',
      name: 'search',
      component: () => import('../views/SearchView.vue'),
      meta: { requiresAuth: true, title: 'Search and filters' },
    },
    {
      path: '/profile',
      name: 'profile',
      component: () => import('../views/ProfileView.vue'),
      meta: { requiresAuth: true, title: 'Profile' },
    },
    {
      path: '/admin/users',
      name: 'admin-users',
      component: () => import('../views/AdminUsersView.vue'),
      meta: { requiresAuth: true, title: 'Users and roles' },
    },
    {
      path: '/projects/:id/legacy',
      name: 'board',
      component: () => import('../views/ProjectBoardView.vue'),
      props: true,
      meta: { requiresAuth: true, title: 'Legacy project board' },
    },
    {
      path: '/projects/:id',
      name: 'workspace',
      component: () => import('../views/ProjectWorkspaceView.vue'),
      props: true,
      meta: { requiresAuth: true, title: 'Project workspace' },
    },
    {
      path: '/:pathMatch(.*)*',
      name: 'not-found',
      redirect: '/projects',
    },
  ],
})

router.beforeEach((to) => {
  const token = getStoredToken()

  if (to.meta.requiresAuth && !token) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }

  if (to.name === 'login' && token) {
    return { name: 'projects' }
  }

  return true
})

router.afterEach((to) => {
  const title = typeof to.meta.title === 'string' ? to.meta.title : ''
  document.title = title ? `${title} · SPM Platform` : 'SPM Platform'
})

export default router
