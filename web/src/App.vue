<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import {
  Activity,
  Cloud,
  DatabaseZap,
  GitCompareArrows,
  ListChecks,
  Menu,
  PanelLeftClose,
  PanelLeftOpen,
  RefreshCw,
  Server,
  Settings,
  X,
} from 'lucide-vue-next'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'

import type { NavigationId } from '@/router'
import { useConsoleStore } from '@/stores/console'
import type { ViewName } from '@/types'

const store = useConsoleStore()
const route = useRoute()
const router = useRouter()
const mobileNavOpen = ref(false)
const mobileMenuButton = ref<HTMLButtonElement | null>(null)
const mobileNav = ref<HTMLElement | null>(null)
const mobileCloseButton = ref<HTMLButtonElement | null>(null)
const mainContent = ref<HTMLElement | null>(null)
const sidebarStorageKey = 'synchub.sidebar.collapsed'

function savedSidebarState(): boolean {
  try {
    return window.localStorage.getItem(sidebarStorageKey) === 'true'
  } catch {
    return false
  }
}

const sidebarCollapsed = ref(savedSidebarState())

const navItems = [
  { id: 'sync' as const, label: '同步工作台', to: '/sync', icon: DatabaseZap },
  { id: 'upstreams' as const, label: '上游连接', to: '/upstreams', icon: Cloud },
  { id: 'targets' as const, label: '目标实例', to: '/targets', icon: Server },
  { id: 'drift' as const, label: '漂移修复', to: '/drift', icon: GitCompareArrows },
  { id: 'tasks' as const, label: '任务记录', to: '/tasks', icon: ListChecks },
  { id: 'settings' as const, label: '系统设置', to: '/settings', icon: Settings },
]

const activeNavigationId = computed(() => route.meta.navigationId as NavigationId | undefined)
const pageTitle = computed(() => route.meta.title ?? '同步工作台')
const driftCount = computed(() => store.driftItems.length)
const healthStatusLabel = computed(() => {
  if (store.healthState === 'loading') return '检测中'
  if (store.healthState === 'ready') return '最近检查正常'
  return '状态未知'
})

const healthStatusClass = computed(() => `is-${store.healthState}`)

function formattedBuildDate(value: string): string {
  const match = value.match(/^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2}:\d{2})/)
  return match ? `${match[1]} ${match[2]}` : value
}

function openMobileNav(): void {
  mobileNavOpen.value = true
  void nextTick(() => mobileCloseButton.value?.focus())
}

function closeMobileNav(focusTarget: 'trigger' | 'content' = 'trigger'): void {
  if (!mobileNavOpen.value) return
  mobileNavOpen.value = false
  void nextTick(() => {
    if (focusTarget === 'content') mainContent.value?.focus()
    else mobileMenuButton.value?.focus()
  })
}

function toggleMobileNav(): void {
  if (mobileNavOpen.value) closeMobileNav()
  else openMobileNav()
}

function toggleDesktopSidebar(): void {
  sidebarCollapsed.value = !sidebarCollapsed.value
  try {
    window.localStorage.setItem(sidebarStorageKey, String(sidebarCollapsed.value))
  } catch {
    // The layout still works when browser storage is unavailable.
  }
}

function closeMobileNavAfterNavigation(): void {
  if (mobileNavOpen.value) closeMobileNav('content')
}

function onKeydown(event: KeyboardEvent): void {
  if (!mobileNavOpen.value) return
  if (event.key === 'Escape') {
    event.preventDefault()
    closeMobileNav()
    return
  }
  if (event.key !== 'Tab' || !mobileNav.value) return

  const focusableItems = [
    ...mobileNav.value.querySelectorAll<HTMLElement>('a[href], button:not([disabled])'),
  ]
  if (focusableItems.length === 0) {
    event.preventDefault()
    mobileNav.value.focus()
    return
  }
  const firstItem = focusableItems[0]
  const lastItem = focusableItems[focusableItems.length - 1]
  const activeElement = document.activeElement
  if (event.shiftKey && (activeElement === firstItem || !mobileNav.value.contains(activeElement))) {
    event.preventDefault()
    lastItem?.focus()
  } else if (!event.shiftKey && (activeElement === lastItem || !mobileNav.value.contains(activeElement))) {
    event.preventDefault()
    firstItem?.focus()
  }
}

function routeForLegacyView(view: ViewName) {
  if (view === 'matrix') return { name: 'sync' }
  if (view === 'drift') return { name: 'drift' }
  if (view === 'settings') return { name: 'settings' }
  return store.selectedTargetId
    ? { name: 'target-channels', params: { id: store.selectedTargetId } }
    : { name: 'targets' }
}

watch(
  () => store.activeView,
  (view) => {
    if (route.meta.legacyView === view) return
    void router.push(routeForLegacyView(view))
  },
)

watch(mobileNavOpen, (isOpen) => {
  document.body.classList.toggle('nav-open', isOpen)
})

watch(
  () => [route.meta.legacyView, route.params.id, store.initialState] as const,
  ([legacyView, routeTargetId, initialState]) => {
    if (legacyView && store.activeView !== legacyView) store.activeView = legacyView
    if (legacyView !== 'channels' || initialState !== 'ready') return

    const targetId = Array.isArray(routeTargetId) ? routeTargetId[0] : routeTargetId
    if (!targetId || !store.targets.some((target) => target.id === targetId)) {
      void router.replace({ name: 'targets' })
      return
    }
    void store.loadChannels(targetId)
  },
  { immediate: true },
)

onMounted(() => {
  document.addEventListener('keydown', onKeydown)
  void store.loadConsole()
})

onUnmounted(() => {
  document.removeEventListener('keydown', onKeydown)
  document.body.classList.remove('nav-open')
})
</script>

<template>
  <a class="skip-link" href="#main-content">跳到主要内容</a>

  <div v-if="store.initialState === 'idle' || store.initialState === 'loading'" class="boot-state">
    <div role="status" aria-label="正在加载控制台">
      <span class="spinner" aria-hidden="true"></span>
      <strong>SyncHub</strong>
      <p>正在加载控制台</p>
    </div>
  </div>

  <div v-else-if="store.initialState === 'error'" class="boot-state boot-error">
    <div role="alert">
      <Activity :size="28" aria-hidden="true" />
      <h1>无法加载控制台</h1>
      <p>{{ store.initialError }}</p>
      <button class="primary-button" type="button" aria-label="重试加载控制台" @click="store.loadConsole">
        重试
      </button>
    </div>
  </div>

  <div v-else class="app-shell" :class="{ 'sidebar-collapsed': sidebarCollapsed }">
    <header class="app-header" aria-label="SyncHub 控制台顶栏">
      <div class="topbar-start">
        <button
          ref="mobileMenuButton"
          class="header-icon-button mobile-menu-button"
          type="button"
          :aria-label="mobileNavOpen ? '关闭导航' : '打开导航'"
          :title="mobileNavOpen ? '关闭导航' : '打开导航'"
          :aria-expanded="mobileNavOpen"
          aria-controls="mobile-primary-navigation"
          @click="toggleMobileNav"
        >
          <PanelLeftClose v-if="mobileNavOpen" :size="19" aria-hidden="true" />
          <Menu v-else :size="19" aria-hidden="true" />
        </button>

        <RouterLink class="topbar-brand" to="/sync" aria-label="SyncHub 首页">
          <span class="brand-mark"><GitCompareArrows :size="18" aria-hidden="true" /></span>
          <strong>SyncHub</strong>
        </RouterLink>

        <button
          class="header-icon-button desktop-sidebar-toggle"
          type="button"
          :aria-label="sidebarCollapsed ? '展开导航' : '收起导航'"
          :title="sidebarCollapsed ? '展开导航' : '收起导航'"
          :aria-expanded="!sidebarCollapsed"
          @click="toggleDesktopSidebar"
        >
          <PanelLeftOpen v-if="sidebarCollapsed" :size="18" aria-hidden="true" />
          <PanelLeftClose v-else :size="18" aria-hidden="true" />
        </button>
      </div>

      <span class="topbar-page-title">{{ pageTitle }}</span>

      <div class="topbar-actions">
        <button
          class="header-icon-button"
          type="button"
          aria-label="刷新控制台"
          title="刷新控制台"
          @click="store.loadConsole"
        >
          <RefreshCw :size="17" aria-hidden="true" />
        </button>
        <div class="topbar-health" role="status" aria-label="本地管理 API" aria-live="polite">
          <span class="health-dot" :class="healthStatusClass" aria-hidden="true"></span>
          <span class="health-context">本地 API</span>
          <strong>{{ healthStatusLabel }}</strong>
        </div>
      </div>
    </header>

    <aside class="desktop-sidebar" aria-label="控制台导航">
      <p class="nav-section-label">工作区</p>
      <nav class="side-nav" aria-label="主导航">
        <RouterLink
          v-for="item in navItems"
          :key="item.id"
          :to="item.to"
          :class="{ active: activeNavigationId === item.id }"
          :aria-current="activeNavigationId === item.id ? 'page' : undefined"
          :title="sidebarCollapsed ? item.label : undefined"
        >
          <component :is="item.icon" :size="18" aria-hidden="true" />
          <span>{{ item.label }}</span>
          <span v-if="item.id === 'drift' && driftCount" class="nav-count" aria-hidden="true">{{ driftCount }}</span>
        </RouterLink>
      </nav>
      <footer class="sidebar-meta" aria-label="构建信息">
        <div class="sidebar-api-state">
          <span class="health-dot" :class="healthStatusClass" aria-hidden="true"></span>
          <span>本地管理 API</span>
          <strong>{{ healthStatusLabel }}</strong>
        </div>
        <span>版本 {{ store.runtimeInfo?.version ?? 'unknown' }}</span>
        <span>
          编译 {{ store.runtimeInfo ? formattedBuildDate(store.runtimeInfo.build_date) : 'unknown' }}
        </span>
      </footer>
    </aside>

    <template v-if="mobileNavOpen">
      <button
        class="nav-scrim"
        type="button"
        tabindex="-1"
        aria-label="关闭导航"
        @click="closeMobileNav()"
      ></button>
      <aside
        id="mobile-primary-navigation"
        ref="mobileNav"
        class="mobile-drawer"
        role="dialog"
        aria-modal="true"
        aria-label="控制台导航"
        tabindex="-1"
      >
        <header class="mobile-drawer-header">
          <div class="drawer-brand" aria-hidden="true">
            <span class="brand-mark"><GitCompareArrows :size="18" /></span>
            <strong>SyncHub</strong>
          </div>
          <button
            ref="mobileCloseButton"
            class="icon-button drawer-close-button"
            type="button"
            aria-label="关闭导航"
            title="关闭导航"
            @click="closeMobileNav()"
          >
            <X :size="19" aria-hidden="true" />
          </button>
        </header>
        <nav class="mobile-nav" aria-label="移动端主导航" data-open="true">
          <p class="nav-section-label">工作区</p>
          <RouterLink
            v-for="item in navItems"
            :key="item.id"
            :to="item.to"
            :class="{ active: activeNavigationId === item.id }"
            :aria-current="activeNavigationId === item.id ? 'page' : undefined"
            @click="closeMobileNavAfterNavigation"
          >
            <component :is="item.icon" :size="18" aria-hidden="true" />
            <span>{{ item.label }}</span>
            <span v-if="item.id === 'drift' && driftCount" class="nav-count" aria-hidden="true">{{ driftCount }}</span>
          </RouterLink>
        </nav>
        <footer class="sidebar-meta" aria-label="构建信息">
          <div class="sidebar-api-state">
            <span class="health-dot" :class="healthStatusClass" aria-hidden="true"></span>
            <span>本地管理 API</span>
            <strong>{{ healthStatusLabel }}</strong>
          </div>
          <span>版本 {{ store.runtimeInfo?.version ?? 'unknown' }}</span>
          <span>
            编译 {{ store.runtimeInfo ? formattedBuildDate(store.runtimeInfo.build_date) : 'unknown' }}
          </span>
        </footer>
      </aside>
    </template>

    <main id="main-content" ref="mainContent" class="app-main" tabindex="-1">
      <RouterView />
    </main>
  </div>
</template>
