import { createPinia } from 'pinia'
import { cleanup, render, screen, waitFor, within } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { createMemoryHistory, createRouter } from 'vue-router'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/api/client'
import { useConsoleStore } from '@/stores/console'
import type { MatrixData, SanitizedConfig, UpstreamAsset } from '@/types'
import DriftPage from './DriftPage.vue'

const targetAlpha = {
  id: 'target-a',
  name: 'Target Alpha',
  type: 'newapi' as const,
  base_url: 'https://target-a.invalid',
}
const targetBeta = {
  id: 'target-b',
  name: 'Target Beta',
  type: 'newapi' as const,
  base_url: 'https://target-b.invalid',
}
const upstreamAlpha = {
  id: 'source-a',
  name: 'Source Alpha',
  type: 'newapi' as const,
  base_url: 'https://source-a.invalid',
}
const upstreamBeta = {
  id: 'source-b',
  name: 'Source Beta',
  type: 'newapi' as const,
  base_url: 'https://source-b.invalid',
}
const config: SanitizedConfig = {
  app: {
    host: '127.0.0.1',
    port: 8888,
    reconcile_interval: '5m0s',
    request_timeout: '15s',
    sync_concurrency: 4,
  },
  targets: [targetAlpha, targetBeta],
  upstreams: [upstreamAlpha, upstreamBeta],
}

function asset(id: string, sourceId: string, name: string): UpstreamAsset {
  return {
    id,
    source_id: sourceId,
    source_type: 'newapi',
    provider: 'openai',
    raw_type: 'OpenAI',
    kind: 'static_api_key',
    name,
    base_url: 'https://provider.invalid',
    models: ['gpt-4.1'],
    enabled: true,
    secret_readable: true,
    metadata: {},
  }
}

function driftMatrix(upstreamId: string): MatrixData {
  return {
    upstream_id: upstreamId,
    refreshed: true,
    targets: [targetAlpha, targetBeta],
    rows: [
      {
        asset: asset(`${upstreamId}:primary`, upstreamId, `${upstreamId} primary`),
        cells: [{
          target_id: targetAlpha.id,
          status: 'drifted',
          channel_id: '42',
          differences: [
            { field: 'weight', expected: 100, actual: 80 },
            { field: 'group', expected: 'default', actual: 'legacy' },
          ],
        }],
      },
      {
        asset: asset(`${upstreamId}:reserve`, upstreamId, `${upstreamId} reserve`),
        cells: [{
          target_id: targetAlpha.id,
          status: 'drifted',
          channel_id: '43',
          differences: [{ field: 'priority', expected: 0, actual: 10 }],
        }],
      },
      {
        asset: asset(`${upstreamId}:beta`, upstreamId, `${upstreamId} beta`),
        cells: [{
          target_id: targetBeta.id,
          status: 'drifted',
          channel_id: '77',
          differences: [{ field: 'models', expected: ['gpt-4.1'], actual: ['gpt-4o-mini'] }],
        }],
      },
    ],
  }
}

async function renderPage(path = '/drift?upstream=source-a&target=target-a') {
  const pinia = createPinia()
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/drift', name: 'drift', component: DriftPage },
      { path: '/upstreams', name: 'upstreams', component: { template: '<div />' } },
      { path: '/targets', name: 'targets', component: { template: '<div />' } },
    ],
  })
  const store = useConsoleStore(pinia)
  store.config = structuredClone(config)
  store.initialState = 'ready'
  store.matrixState = 'ready'
  store.selectedUpstreamId = upstreamAlpha.id
  store.selectedTargetId = targetAlpha.id
  store.matrix = driftMatrix(upstreamAlpha.id)
  await router.push(path)
  await router.isReady()

  return {
    ...render(DriftPage, { global: { plugins: [pinia, router] } }),
    router,
    store,
  }
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('drift repair workflow', () => {
  it('loads the selected upstream and filters drift rows by URL-backed selectors', async () => {
    const user = userEvent.setup()
    const { router, store } = await renderPage()
    const loadMatrix = vi.spyOn(store, 'loadMatrix').mockImplementation(async (upstreamId = '') => {
      store.selectedUpstreamId = upstreamId
      store.matrix = driftMatrix(upstreamId)
      store.matrixState = 'ready'
    })

    expect(screen.getByRole('combobox', { name: '选择上游' })).toHaveValue('source-a')
    expect(screen.getByRole('combobox', { name: '选择目标' })).toHaveValue('target-a')
    expect(screen.getByRole('article', { name: 'source-a primary / Target Alpha 配置漂移' })).toBeInTheDocument()
    expect(screen.queryByRole('article', { name: 'source-a beta / Target Beta 配置漂移' })).not.toBeInTheDocument()

    await user.selectOptions(screen.getByRole('combobox', { name: '选择目标' }), targetBeta.id)

    await waitFor(() => expect(router.currentRoute.value.query).toEqual({
      upstream: upstreamAlpha.id,
      target: targetBeta.id,
    }))
    expect(screen.getByRole('article', { name: 'source-a beta / Target Beta 配置漂移' })).toBeInTheDocument()
    expect(screen.queryByRole('article', { name: 'source-a primary / Target Alpha 配置漂移' })).not.toBeInTheDocument()
    expect(loadMatrix).not.toHaveBeenCalled()

    await user.selectOptions(screen.getByRole('combobox', { name: '选择上游' }), upstreamBeta.id)

    await waitFor(() => expect(loadMatrix).toHaveBeenCalledWith(upstreamBeta.id))
    await waitFor(() => expect(router.currentRoute.value.query).toEqual({
      upstream: upstreamBeta.id,
      target: targetBeta.id,
    }))
    expect(screen.getByRole('article', { name: 'source-b beta / Target Beta 配置漂移' })).toBeInTheDocument()
  })

  it('confirms restoring expected state and revalidates the selected upstream', async () => {
    const user = userEvent.setup()
    const { store } = await renderPage()
    const restoreDrift = vi.spyOn(api, 'restoreDrift').mockResolvedValue({})
    const loadMatrix = vi.spyOn(store, 'loadMatrix').mockResolvedValue()
    const markDriftAccepted = vi.spyOn(store, 'markDriftAccepted')
    const row = screen.getByRole('article', { name: 'source-a primary / Target Alpha 配置漂移' })

    await user.click(within(row).getByRole('button', { name: '恢复 source-a primary 在 Target Alpha 的期望状态' }))

    const dialog = screen.getByRole('dialog', { name: '确认恢复期望状态' })
    expect(restoreDrift).not.toHaveBeenCalled()
    expect(dialog).toHaveTextContent('source-a primary')
    expect(dialog).toHaveTextContent('Target Alpha')
    expect(dialog).toHaveTextContent('将上游期望状态重新写入目标平台，覆盖目标端当前状态。')
    expect(dialog).toHaveTextContent('2 项字段差异')

    await user.click(within(dialog).getByRole('button', { name: '确认恢复期望状态' }))

    await waitFor(() => expect(restoreDrift).toHaveBeenCalledWith(targetAlpha.id, {
      upstream_asset_id: 'source-a:primary',
      channel_id: '42',
    }))
    expect(loadMatrix).toHaveBeenCalledWith(upstreamAlpha.id)
    expect(markDriftAccepted).not.toHaveBeenCalled()
    expect(await screen.findByRole('status')).toHaveTextContent('期望状态已恢复')
  })

  it('confirms accepting target state and revalidates instead of clearing local drift', async () => {
    const user = userEvent.setup()
    const { store } = await renderPage()
    const acceptDrift = vi.spyOn(api, 'acceptDrift').mockResolvedValue({})
    const loadMatrix = vi.spyOn(store, 'loadMatrix').mockResolvedValue()
    const markDriftAccepted = vi.spyOn(store, 'markDriftAccepted')
    const row = screen.getByRole('article', { name: 'source-a primary / Target Alpha 配置漂移' })

    await user.click(within(row).getByRole('button', { name: '采纳 source-a primary 在 Target Alpha 的目标状态' }))

    const dialog = screen.getByRole('dialog', { name: '确认采纳目标状态' })
    expect(acceptDrift).not.toHaveBeenCalled()
    expect(dialog).toHaveTextContent('source-a primary')
    expect(dialog).toHaveTextContent('Target Alpha')
    expect(dialog).toHaveTextContent('将目标平台当前状态采纳为后续同步基线，不会写回上游期望值。')
    expect(dialog).toHaveTextContent('2 项字段差异')

    await user.click(within(dialog).getByRole('button', { name: '确认采纳目标状态' }))

    await waitFor(() => expect(acceptDrift).toHaveBeenCalledWith(targetAlpha.id, {
      upstream_asset_id: 'source-a:primary',
      channel_id: '42',
    }))
    expect(loadMatrix).toHaveBeenCalledWith(upstreamAlpha.id)
    expect(markDriftAccepted).not.toHaveBeenCalled()
    expect(await screen.findByRole('status')).toHaveTextContent('目标状态已采纳')
  })

  it('scopes pending actions per asset and sanitizes unexpected failures', async () => {
    const user = userEvent.setup()
    const { store } = await renderPage()
    let rejectRestore: (reason?: unknown) => void = () => undefined
    const pendingRestore = new Promise<Record<string, unknown>>((_, reject) => {
      rejectRestore = reject
    })
    vi.spyOn(api, 'restoreDrift').mockReturnValue(pendingRestore)
    const loadMatrix = vi.spyOn(store, 'loadMatrix').mockResolvedValue()
    const primary = screen.getByRole('article', { name: 'source-a primary / Target Alpha 配置漂移' })
    const reserve = screen.getByRole('article', { name: 'source-a reserve / Target Alpha 配置漂移' })

    await user.click(within(primary).getByRole('button', { name: '恢复 source-a primary 在 Target Alpha 的期望状态' }))
    await user.click(within(screen.getByRole('dialog', { name: '确认恢复期望状态' }))
      .getByRole('button', { name: '确认恢复期望状态' }))

    await waitFor(() => {
      expect(within(primary).getByRole('button', { name: '恢复 source-a primary 在 Target Alpha 的期望状态' })).toBeDisabled()
      expect(within(primary).getByRole('button', { name: '采纳 source-a primary 在 Target Alpha 的目标状态' })).toBeDisabled()
    })
    expect(within(reserve).getByRole('button', { name: '恢复 source-a reserve 在 Target Alpha 的期望状态' })).toBeEnabled()
    expect(within(reserve).getByRole('button', { name: '采纳 source-a reserve 在 Target Alpha 的目标状态' })).toBeEnabled()

    rejectRestore(new Error('private target response with sk-live-secret'))

    expect(await screen.findByRole('alert')).toHaveTextContent('操作未完成，请重试')
    expect(screen.queryByText(/sk-live-secret/)).not.toBeInTheDocument()
    expect(loadMatrix).not.toHaveBeenCalled()
  })
})
