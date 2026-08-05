/// <reference types="node" />

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

import { createPinia, setActivePinia } from 'pinia'
import { render, screen, within } from '@testing-library/vue'
import { beforeEach, describe, expect, it } from 'vitest'

import { useConsoleStore } from '@/stores/console'
import type { Channel, MatrixData, SanitizedConfig } from '@/types'
import ChannelsPage from './ChannelsPage.vue'
import DriftPage from './DriftPage.vue'
import MatrixPage from './MatrixPage.vue'
import SettingsPage from './SettingsPage.vue'
import TasksPage from './TasksPage.vue'

const appSettings = {
  host: '127.0.0.1',
  port: 8888,
  reconcile_interval: '5m0s',
  request_timeout: '15s',
  sync_concurrency: 4,
}
const target = {
  id: 'target-a',
  name: 'Target Alpha',
  type: 'newapi' as const,
  base_url: 'https://target.invalid',
}
const upstream = {
  id: 'source-a',
  name: 'Source Alpha',
  type: 'newapi' as const,
  base_url: 'https://source.invalid',
}
const configured: SanitizedConfig = {
  app: appSettings,
  targets: [target],
  upstreams: [upstream],
}
const driftMatrix: MatrixData = {
  upstream_id: upstream.id,
  refreshed: true,
  targets: [target],
  rows: [{
    asset: {
      id: 'asset-a',
      source_id: upstream.id,
      source_type: 'newapi',
      provider: 'openai',
      raw_type: 'OpenAI',
      kind: 'static_api_key',
      name: 'Primary key',
      base_url: 'https://provider.invalid',
      models: ['gpt-4.1'],
      enabled: true,
      secret_readable: true,
      metadata: {},
    },
    cells: [{
      target_id: target.id,
      status: 'drifted',
      channel_id: '42',
      differences: [{ field: 'weight', expected: 100, actual: 80 }],
    }],
  }],
}
const channels: Channel[] = [
  {
    id: '42',
    name: 'Managed channel',
    provider: 'openai',
    raw_type: '1',
    base_url: 'https://provider.invalid',
    models: ['gpt-4.1'],
    group: 'default',
    priority: 0,
    weight: 100,
    enabled: true,
    managed: true,
    upstream_asset_id: 'asset-a',
  },
  {
    id: '43',
    name: 'Native channel',
    provider: 'openai',
    raw_type: '1',
    base_url: 'https://native.invalid',
    models: ['gpt-4.1-mini'],
    group: 'default',
    priority: 0,
    weight: 100,
    enabled: false,
    managed: false,
  },
]

function setupStore(config: SanitizedConfig = configured) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const store = useConsoleStore()
  store.config = config
  store.initialState = 'ready'
  store.matrixState = 'ready'
  store.channelState = 'ready'
  store.selectedUpstreamId = config.upstreams[0]?.id ?? ''
  store.selectedTargetId = config.targets[0]?.id ?? ''
  return { pinia, store }
}

describe('workspace page information architecture', () => {
  beforeEach(() => {
    window.localStorage.clear()
    window.sessionStorage.clear()
  })

  it('turns the empty matrix into a short first-sync checklist', () => {
    const { pinia } = setupStore({ ...configured, targets: [], upstreams: [] })
    render(MatrixPage, { global: { plugins: [pinia] } })

    expect(screen.getByRole('heading', { name: '资产矩阵' })).toHaveTextContent('同步工作台')
    const checklist = screen.getByRole('list', { name: '首次同步步骤' })
    expect(within(checklist).getAllByRole('listitem')).toHaveLength(4)
    expect(checklist).toHaveTextContent('配置目标实例')
    expect(checklist).toHaveTextContent('配置上游连接')
    expect(checklist).toHaveTextContent('刷新来源资产')
    expect(checklist).toHaveTextContent('选择资产并同步')
  })

  it('summarizes managed and native target channels without changing table semantics', () => {
    const { pinia, store } = setupStore()
    store.channels = channels
    render(ChannelsPage, { global: { plugins: [pinia] } })

    const summary = screen.getByRole('region', { name: '渠道概览' })
    expect(summary).toHaveTextContent('2 个渠道')
    expect(summary).toHaveTextContent('1 个托管')
    expect(summary).toHaveTextContent('1 个原生')
    expect(screen.getByRole('table')).toBeInTheDocument()
  })

  it('presents drift as a pending scan queue while retaining its accessible heading', () => {
    const { pinia, store } = setupStore()
    store.matrix = driftMatrix
    render(DriftPage, { global: { plugins: [pinia] } })

    expect(screen.getByRole('heading', { name: '配置漂移' })).toHaveTextContent('漂移修复')
    expect(screen.getByRole('toolbar', { name: '漂移扫描' })).toHaveTextContent('1 项待处理')
    expect(screen.getByRole('button', { name: '校验全部目标' })).toBeEnabled()
  })

  it('uses an honest empty task table with type and status columns', () => {
    render(TasksPage, {
      props: { loading: false },
      global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } },
    })

    const table = screen.getByRole('table', { name: '任务状态列表' })
    expect(within(table).getByRole('columnheader', { name: '任务类型' })).toBeInTheDocument()
    expect(within(table).getByRole('columnheader', { name: '范围' })).toBeInTheDocument()
    expect(within(table).getByRole('columnheader', { name: '状态' })).toBeInTheDocument()
    expect(within(table).getByRole('columnheader', { name: '开始时间' })).toBeInTheDocument()
    expect(within(table).getByText('暂无任务记录')).toBeInTheDocument()
  })

  it('keeps settings categories in unframed configuration sections', () => {
    const { pinia } = setupStore()
    render(SettingsPage, { global: { plugins: [pinia] } })

    expect(screen.getByRole('heading', { name: '设置' })).toHaveTextContent('系统设置')
    expect(screen.getByRole('tablist', { name: '设置分类' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: '实例与连接配置' })).toBeInTheDocument()
  })
})

describe('workspace page responsive layout contracts', () => {
  const pageSources = [
    'MatrixPage.vue',
    'ChannelsPage.vue',
    'DriftPage.vue',
    'TasksPage.vue',
    'SettingsPage.vue',
  ].map((file) => readFileSync(resolve(process.cwd(), 'src/pages', file), 'utf8'))

  it('keeps all workspace layout CSS scoped to its page', () => {
    for (const source of pageSources) expect(source).toContain('<style scoped>')
  })

  it('keeps the sync selection dock sticky and mobile-safe', () => {
    const matrixPage = pageSources[0]
    expect(matrixPage).toContain('class="selection-dock"')
    expect(matrixPage).toMatch(/\.selection-dock\s*\{[^}]*position:\s*sticky;/s)
    expect(matrixPage).toContain('env(safe-area-inset-bottom)')
  })

  it('defines page-owned mobile information-row layouts', () => {
    for (const source of pageSources.slice(0, 4)) {
      expect(source).toContain('@media (max-width: 620px)')
    }
  })

  it('removes card framing from settings sections', () => {
    const settingsPage = pageSources[4]
    expect(settingsPage).toMatch(/\.settings-band\s*\{[^}]*border:\s*0;/s)
    expect(settingsPage).toMatch(/\.settings-band\s*\{[^}]*box-shadow:\s*none;/s)
  })
})
