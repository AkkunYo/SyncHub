import { createPinia, setActivePinia } from 'pinia'
import { render, screen, within } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { createMemoryHistory, createRouter } from 'vue-router'
import { describe, expect, it } from 'vitest'

import { useConsoleStore } from '@/stores/console'
import type { Channel, SanitizedConfig } from '@/types'

import ChannelsPage from './ChannelsPage.vue'

const target = {
  id: 'target-a',
  name: 'Target Alpha',
  type: 'newapi' as const,
  base_url: 'https://target.invalid',
}

const config: SanitizedConfig = {
  app: {
    host: '127.0.0.1',
    port: 8888,
    reconcile_interval: '5m0s',
    request_timeout: '15s',
    sync_concurrency: 4,
  },
  targets: [target],
  upstreams: [],
}

function makeChannels(count: number): Channel[] {
  return Array.from({ length: count }, (_, index) => ({
    id: String(index + 1),
    name: `Channel ${index + 1}`,
    provider: 'openai',
    raw_type: '1',
    base_url: 'https://provider.invalid',
    models: ['gpt-4.1'],
    group: 'default',
    priority: 0,
    weight: 100,
    enabled: true,
    managed: index % 2 === 0,
    upstream_asset_id: index % 2 === 0 ? `asset-${index + 1}` : undefined,
  }))
}

async function renderChannels(initialPath = '/targets/target-a/channels') {
  const pinia = createPinia()
  setActivePinia(pinia)
  const store = useConsoleStore()
  store.config = config
  store.initialState = 'ready'
  store.channelState = 'ready'
  store.selectedTargetId = target.id
  store.channels = makeChannels(21)

  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/targets/:id/channels', name: 'target-channels', component: ChannelsPage }],
  })
  await router.push(initialPath)
  await router.isReady()
  render(ChannelsPage, { global: { plugins: [pinia, router] } })
  return { router }
}

describe('ChannelsPage pagination', () => {
  it('shows one page of channels and advances the URL-backed page', async () => {
    const user = userEvent.setup()
    const { router } = await renderChannels('/targets/target-a/channels?page=2')

    const table = screen.getByRole('table')
    expect(within(table).getAllByRole('row')).toHaveLength(11)
    expect(within(table).getByText('Channel 11')).toBeInTheDocument()
    expect(within(table).queryByText('Channel 1')).not.toBeInTheDocument()
    expect(screen.getByText('显示 11-20 / 21 个渠道')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '下一页' }))
    expect(router.currentRoute.value.query.page).toBe('3')
    expect(within(screen.getByRole('table')).getAllByRole('row')).toHaveLength(2)
    expect(screen.getByText('Channel 21')).toBeInTheDocument()
  })
})
