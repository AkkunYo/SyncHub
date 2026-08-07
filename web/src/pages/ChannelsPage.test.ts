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
}

describe('ChannelsPage pagination', () => {
  it('shows one page of channels and advances the URL-backed page', async () => {
    const user = userEvent.setup()
    await renderChannels('/targets/target-a/channels?page=2')

    const table = screen.getByRole('table')
    expect(within(table).getAllByRole('row')).toHaveLength(11)
    expect(within(table).getByText('Channel 11')).toBeInTheDocument()
    expect(within(table).queryByText('Channel 1')).not.toBeInTheDocument()
    expect(screen.getByText('显示 11-20 / 21 个渠道')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '下一页' }))
    expect(new URL(window.location.href).searchParams.get('page')).toBe('3')
    expect(within(screen.getByRole('table')).getAllByRole('row')).toHaveLength(2)
    expect(screen.getByText('Channel 21')).toBeInTheDocument()
  })
})

describe('ChannelsPage native channel mutation guard', () => {
  it('keeps managed channel edit and delete actions on the normal confirmation path', async () => {
    const user = userEvent.setup()
    await renderChannels()

    await user.click(screen.getByRole('button', { name: '编辑渠道 Channel 1' }))

    let dialog = screen.getByRole('dialog', { name: '编辑目标渠道' })
    expect(within(dialog).queryByText(/直接写入目标平台/)).not.toBeInTheDocument()
    expect(within(dialog).queryByRole('checkbox', { name: /确认直接修改目标平台/ })).not.toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: '保存渠道' })).toBeEnabled()

    await user.click(within(dialog).getByRole('button', { name: '取消' }))
    await user.click(screen.getByRole('button', { name: '删除渠道 Channel 1' }))

    dialog = screen.getByRole('dialog', { name: '删除目标渠道' })
    expect(within(dialog).queryByText(/直接写入目标平台/)).not.toBeInTheDocument()
    expect(within(dialog).queryByRole('checkbox', { name: /确认直接删除目标平台渠道/ })).not.toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: '确认删除' })).toBeEnabled()
  })

  it('marks native edits as dangerous and requires a fresh explicit confirmation', async () => {
    const user = userEvent.setup()
    await renderChannels()

    const editAction = screen.getByRole('button', { name: '危险编辑原生渠道 Channel 2' })
    expect(within(editAction).getByText('危险编辑')).toBeInTheDocument()
    await user.click(editAction)

    let dialog = screen.getByRole('dialog', { name: '危险操作：编辑目标原生渠道' })
    expect(within(dialog).getByRole('alert')).toHaveTextContent(
      '此操作会直接写入目标平台，且不会创建 SyncHub 同步映射。',
    )
    let confirmation = within(dialog).getByRole('checkbox', { name: '我了解风险，确认直接修改目标平台' })
    let save = within(dialog).getByRole('button', { name: '确认直接保存' })
    expect(confirmation).not.toBeChecked()
    expect(save).toBeDisabled()

    await user.click(confirmation)
    expect(save).toBeEnabled()
    await user.click(within(dialog).getByRole('button', { name: '取消' }))
    await user.click(screen.getByRole('button', { name: '危险编辑原生渠道 Channel 2' }))

    dialog = screen.getByRole('dialog', { name: '危险操作：编辑目标原生渠道' })
    confirmation = within(dialog).getByRole('checkbox', { name: '我了解风险，确认直接修改目标平台' })
    save = within(dialog).getByRole('button', { name: '确认直接保存' })
    expect(confirmation).not.toBeChecked()
    expect(save).toBeDisabled()
  })

  it('marks native deletes as dangerous and resets confirmation when the modal changes', async () => {
    const user = userEvent.setup()
    await renderChannels()

    const deleteAction = screen.getByRole('button', { name: '危险删除原生渠道 Channel 2' })
    expect(within(deleteAction).getByText('危险删除')).toBeInTheDocument()
    await user.click(deleteAction)

    let dialog = screen.getByRole('dialog', { name: '危险操作：删除目标原生渠道' })
    expect(within(dialog).getByRole('alert')).toHaveTextContent(
      '此操作会直接写入目标平台，且不会创建 SyncHub 同步映射。',
    )
    const confirmation = within(dialog).getByRole('checkbox', { name: '我了解风险，确认直接删除目标平台渠道' })
    expect(within(dialog).getByRole('button', { name: '确认直接删除' })).toBeDisabled()

    await user.click(confirmation)
    expect(within(dialog).getByRole('button', { name: '确认直接删除' })).toBeEnabled()
    await user.click(within(dialog).getByRole('button', { name: '取消' }))
    await user.click(screen.getByRole('button', { name: '危险编辑原生渠道 Channel 2' }))

    dialog = screen.getByRole('dialog', { name: '危险操作：编辑目标原生渠道' })
    expect(within(dialog).getByRole('checkbox', { name: '我了解风险，确认直接修改目标平台' })).not.toBeChecked()
    expect(within(dialog).getByRole('button', { name: '确认直接保存' })).toBeDisabled()
  })
})
