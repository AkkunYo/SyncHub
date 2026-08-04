import {
  createRouter,
  createWebHistory,
  type Router,
  type RouterHistory,
  type RouteRecordRaw,
} from 'vue-router'

import ChannelsPage from '@/pages/ChannelsPage.vue'
import DriftPage from '@/pages/DriftPage.vue'
import MatrixPage from '@/pages/MatrixPage.vue'
import SettingsPage from '@/pages/SettingsPage.vue'
import TargetsPage from '@/pages/TargetsPage.vue'
import TasksPage from '@/pages/TasksPage.vue'
import UpstreamsPage from '@/pages/UpstreamsPage.vue'
import type { ViewName } from '@/types'

export type NavigationId = 'sync' | 'upstreams' | 'targets' | 'drift' | 'tasks' | 'settings'

declare module 'vue-router' {
  interface RouteMeta {
    navigationId?: NavigationId
    legacyView?: ViewName
    title?: string
  }
}

export const consoleRoutes: RouteRecordRaw[] = [
  { path: '/', redirect: { name: 'sync' } },
  {
    path: '/sync',
    name: 'sync',
    component: MatrixPage,
    meta: { navigationId: 'sync', legacyView: 'matrix', title: '同步工作台' },
  },
  {
    path: '/upstreams',
    name: 'upstreams',
    component: UpstreamsPage,
    meta: { navigationId: 'upstreams', legacyView: 'settings', title: '上游连接' },
  },
  {
    path: '/targets',
    name: 'targets',
    component: TargetsPage,
    meta: { navigationId: 'targets', legacyView: 'settings', title: '目标实例' },
  },
  {
    path: '/targets/:id/channels',
    name: 'target-channels',
    component: ChannelsPage,
    meta: { navigationId: 'targets', legacyView: 'channels', title: '目标渠道' },
  },
  {
    path: '/drift',
    name: 'drift',
    component: DriftPage,
    meta: { navigationId: 'drift', legacyView: 'drift', title: '漂移修复' },
  },
  {
    path: '/tasks',
    name: 'tasks',
    component: TasksPage,
    props: { loading: false },
    meta: { navigationId: 'tasks', title: '任务记录' },
  },
  {
    path: '/settings',
    name: 'settings',
    component: SettingsPage,
    meta: { navigationId: 'settings', legacyView: 'settings', title: '系统设置' },
  },
  { path: '/:pathMatch(.*)*', redirect: { name: 'sync' } },
]

export function createAppRouter(history: RouterHistory = createWebHistory()): Router {
  const router = createRouter({
    history,
    routes: consoleRoutes,
    scrollBehavior: () => ({ top: 0 }),
  })
  router.afterEach((route) => {
    document.title = route.meta.title ? `${route.meta.title} | SyncHub` : 'SyncHub'
  })
  return router
}

export const router = createAppRouter()
