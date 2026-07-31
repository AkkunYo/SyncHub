<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import {
  Activity,
  DatabaseZap,
  GitCompareArrows,
  Menu,
  PanelLeftClose,
  RadioTower,
  Settings,
} from 'lucide-vue-next'

import ChannelsPage from '@/pages/ChannelsPage.vue'
import DriftPage from '@/pages/DriftPage.vue'
import MatrixPage from '@/pages/MatrixPage.vue'
import SettingsPage from '@/pages/SettingsPage.vue'
import { useConsoleStore } from '@/stores/console'
import type { ViewName } from '@/types'

const store = useConsoleStore()
const mobileNavOpen = ref(false)
const mobileMenuButton = ref<HTMLButtonElement | null>(null)

const navItems = [
  { id: 'matrix' as const, label: '资产矩阵', icon: DatabaseZap },
  { id: 'channels' as const, label: '目标渠道', icon: RadioTower },
  { id: 'drift' as const, label: '漂移', icon: GitCompareArrows },
  { id: 'settings' as const, label: '设置', icon: Settings },
]

const driftCount = computed(() => store.driftItems.length)
const activeNavLabel = computed(
  () => navItems.find((item) => item.id === store.activeView)?.label ?? '资产矩阵',
)

function formattedBuildDate(value: string): string {
  const match = value.match(/^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2}:\d{2})/)
  return match ? `${match[1]} ${match[2]}` : value
}

function navigate(view: ViewName): void {
  store.navigate(view)
  mobileNavOpen.value = false
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key !== 'Escape' || !mobileNavOpen.value) return
  mobileNavOpen.value = false
  void nextTick(() => mobileMenuButton.value?.focus())
}

onMounted(() => {
  document.addEventListener('keydown', onKeydown)
  void store.loadConsole()
})

onUnmounted(() => {
  document.removeEventListener('keydown', onKeydown)
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

  <div v-else class="app-shell">
    <header class="app-header" aria-label="SyncHub 控制台顶栏">
      <button
        ref="mobileMenuButton"
        class="icon-button mobile-menu-button"
        type="button"
        :aria-label="mobileNavOpen ? '关闭导航' : '打开导航'"
        :title="mobileNavOpen ? '关闭导航' : '打开导航'"
        :aria-expanded="mobileNavOpen"
        @click="mobileNavOpen = !mobileNavOpen"
      >
        <PanelLeftClose v-if="mobileNavOpen" :size="19" aria-hidden="true" />
        <Menu v-else :size="19" aria-hidden="true" />
      </button>

      <div class="topbar-brand">
        <span class="brand-mark"><GitCompareArrows :size="18" aria-hidden="true" /></span>
        <strong>SyncHub</strong>
      </div>

      <div class="topbar-context">
        <span>同步工作台</span>
        <strong>{{ activeNavLabel }}</strong>
      </div>

      <div class="topbar-health" aria-label="本地管理 API">
        <span>本地管理</span>
      </div>
    </header>

    <aside class="desktop-sidebar">
      <div class="sidebar-intro">
        <span>管理控制台</span>
        <strong>资源同步中心</strong>
      </div>
      <p class="nav-section-label">工作区</p>
      <nav class="side-nav" aria-label="主导航">
        <button
          v-for="item in navItems"
          :key="item.id"
          type="button"
          :class="{ active: store.activeView === item.id }"
          :aria-current="store.activeView === item.id ? 'page' : undefined"
          @click="navigate(item.id)"
        >
          <component :is="item.icon" :size="18" aria-hidden="true" />
          <span>{{ item.label }}</span>
          <span v-if="item.id === 'drift' && driftCount" class="nav-count" aria-hidden="true">{{ driftCount }}</span>
        </button>
      </nav>
      <div class="sidebar-status">
        <span class="health-dot" aria-hidden="true"></span>
        <div>
          <span class="status-label">运行状态</span>
          <strong>本地管理 API</strong>
          <small v-if="store.runtimeInfo">版本 {{ store.runtimeInfo.version }}</small>
          <small v-if="store.runtimeInfo">编译 {{ formattedBuildDate(store.runtimeInfo.build_date) }}</small>
        </div>
      </div>
    </aside>

    <template v-if="mobileNavOpen">
      <button class="nav-scrim" type="button" aria-label="关闭导航" @click="mobileNavOpen = false"></button>
      <nav class="mobile-nav" aria-label="移动端主导航" data-open="true">
        <button
          v-for="item in navItems"
          :key="item.id"
          type="button"
          :class="{ active: store.activeView === item.id }"
          :aria-current="store.activeView === item.id ? 'page' : undefined"
          @click="navigate(item.id)"
        >
          <component :is="item.icon" :size="18" aria-hidden="true" />
          <span>{{ item.label }}</span>
          <span v-if="item.id === 'drift' && driftCount" class="nav-count" aria-hidden="true">{{ driftCount }}</span>
        </button>
      </nav>
    </template>

    <main id="main-content" class="app-main" tabindex="-1">
      <MatrixPage v-if="store.activeView === 'matrix'" />
      <ChannelsPage v-else-if="store.activeView === 'channels'" />
      <DriftPage v-else-if="store.activeView === 'drift'" />
      <SettingsPage v-else />
    </main>
  </div>
</template>
