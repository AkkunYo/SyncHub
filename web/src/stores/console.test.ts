import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, expectTypeOf, it, vi } from 'vitest'

import { api } from '@/api/client'
import { useConsoleStore } from './console'
import type { Channel, MatrixData, SanitizedConfig, SyncTargetResult } from '@/types'

const target = { id: 'target-a', name: 'Target A', type: 'newapi' as const, base_url: 'https://target.invalid' }
const upstream = {
  id: 'source-a',
  name: 'Source A',
  type: 'newapi' as const,
  base_url: 'https://source.invalid',
}
const upstreamB = {
  ...upstream,
  id: 'source-b',
  name: 'Source B',
}
const settings = {
  host: '127.0.0.1',
  port: 8888,
  reconcile_interval: '5m0s',
  request_timeout: '15s',
  sync_concurrency: 4,
}
const sanitizedConfig: SanitizedConfig = { app: settings, targets: [target], upstreams: [upstream] }
const emptyMatrix: MatrixData = {
  upstream_id: 'source-a',
  refreshed: true,
  targets: [target],
  rows: [],
}
function matrixWithDrift(upstreamId = upstream.id): MatrixData {
  return {
    ...emptyMatrix,
    upstream_id: upstreamId,
    rows: [{
      asset: {
        id: 'asset-a',
        source_id: upstreamId,
        source_type: 'newapi',
        provider: 'openai',
        raw_type: 'OpenAI',
        kind: 'static_api_key',
        name: 'Asset A',
        base_url: '',
        models: ['model-a'],
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
}
const channel: Channel = {
  id: '42',
  name: 'Channel A',
  provider: 'openai',
  raw_type: '1',
  base_url: '',
  models: ['model-a'],
  group: 'default',
  priority: 0,
  weight: 100,
  enabled: true,
  managed: false,
}

describe('console store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  it('keeps synchronization result statuses narrower than matrix statuses', () => {
    expectTypeOf<SyncTargetResult['status']>().toEqualTypeOf<
      'synced' | 'incompatible' | 'needs_reconcile' | 'failed'
    >()
  })

  it('loads sanitized configuration and handles sources with and without matrix data', async () => {
    vi.spyOn(api, 'getConfig').mockResolvedValue(sanitizedConfig)
    vi.spyOn(api, 'getMatrix').mockResolvedValue(emptyMatrix)
    const store = useConsoleStore()

    await store.loadConsole()

    expect(store.initialState).toBe('ready')
    expect(store.selectedTargetId).toBe('target-a')
    expect(store.selectedUpstreamId).toBe('source-a')

    vi.spyOn(api, 'getConfig').mockResolvedValue({ ...sanitizedConfig, upstreams: [] })
    await store.loadConsole()
    expect(store.matrix).toBeNull()
    expect(store.matrixState).toBe('ready')
  })

  it('keeps sanitized loading errors and supports matrix refresh recovery', async () => {
    const store = useConsoleStore()
    store.config = sanitizedConfig
    store.selectedUpstreamId = 'source-a'
    vi.spyOn(api, 'getMatrix').mockRejectedValueOnce(new Error('transport detail')).mockResolvedValue(emptyMatrix)

    await expect(store.loadMatrix()).rejects.toThrow()
    expect(store.matrixError).toBe('操作未完成，请重试')
    expect(store.matrixState).toBe('error')

    vi.spyOn(api, 'refreshUpstream').mockResolvedValue({ refreshed: true })
    await store.refreshAssets()
    expect(store.matrixState).toBe('ready')

    vi.mocked(api.refreshUpstream).mockRejectedValueOnce(new Error('transport detail'))
    await store.refreshAssets()
    expect(store.matrixState).toBe('error')
  })

  it('loads channel success, error, and no-target states', async () => {
    const store = useConsoleStore()
    store.config = sanitizedConfig
    vi.spyOn(api, 'getChannels').mockResolvedValueOnce({ channels: [channel] }).mockRejectedValueOnce(new Error('detail'))

    await store.loadChannels('target-a')
    expect(store.channels).toEqual([channel])
    await store.loadChannels('target-a')
    expect(store.channelState).toBe('error')
    expect(store.channelError).toBe('操作未完成，请重试')
    await store.loadChannels('')
    expect(store.channels).toEqual([])
    expect(store.channelState).toBe('ready')
  })

  it('ignores stale matrix and channel responses after the selection changes', async () => {
    const store = useConsoleStore()
    const targetB = { ...target, id: 'target-b', name: 'Target B' }
    store.config = {
      ...sanitizedConfig,
      targets: [target, targetB],
      upstreams: [upstream, upstreamB],
    }

    let resolveMatrixA: ((value: MatrixData) => void) | undefined
    let resolveMatrixB: ((value: MatrixData) => void) | undefined
    const matrixA = new Promise<MatrixData>((resolve) => { resolveMatrixA = resolve })
    const matrixB = new Promise<MatrixData>((resolve) => { resolveMatrixB = resolve })
    vi.spyOn(api, 'getMatrix').mockImplementation((sourceId) => sourceId === upstream.id ? matrixA : matrixB)

    const firstMatrix = store.loadMatrix(upstream.id)
    const secondMatrix = store.loadMatrix(upstreamB.id)
    resolveMatrixB?.({ ...emptyMatrix, upstream_id: upstreamB.id })
    await secondMatrix
    resolveMatrixA?.(emptyMatrix)
    await firstMatrix

    expect(store.selectedUpstreamId).toBe(upstreamB.id)
    expect(store.matrix?.upstream_id).toBe(upstreamB.id)

    let resolveChannelsA: ((value: { channels: Channel[] }) => void) | undefined
    let resolveChannelsB: ((value: { channels: Channel[] }) => void) | undefined
    const channelsA = new Promise<{ channels: Channel[] }>((resolve) => { resolveChannelsA = resolve })
    const channelsB = new Promise<{ channels: Channel[] }>((resolve) => { resolveChannelsB = resolve })
    vi.spyOn(api, 'getChannels').mockImplementation((targetId) => targetId === target.id ? channelsA : channelsB)

    const firstChannels = store.loadChannels(target.id)
    const secondChannels = store.loadChannels(targetB.id)
    resolveChannelsB?.({ channels: [{ ...channel, id: '84', name: 'Target B channel' }] })
    await secondChannels
    resolveChannelsA?.({ channels: [channel] })
    await firstChannels

    expect(store.selectedTargetId).toBe(targetB.id)
    expect(store.channels).toEqual([{ ...channel, id: '84', name: 'Target B channel' }])
  })

  it('only exposes drift for the ready matrix of the selected upstream', () => {
    const store = useConsoleStore()
    store.selectedUpstreamId = upstream.id
    store.matrix = matrixWithDrift()
    store.matrixState = 'ready'

    expect(store.driftItems).toHaveLength(1)
    for (const state of ['idle', 'loading', 'error'] as const) {
      store.matrixState = state
      expect(store.driftItems).toEqual([])
    }

    store.matrixState = 'ready'
    store.selectedUpstreamId = upstreamB.id
    expect(store.driftItems).toEqual([])
  })

  it('rejects sync results for a different expected upstream without changing the matrix', () => {
    const store = useConsoleStore()
    const original = matrixWithDrift()
    store.selectedUpstreamId = upstream.id
    store.matrix = structuredClone(original)
    store.matrixState = 'ready'

    const applied = store.applySyncResults([{
      assetId: 'asset-a',
      assetName: 'Asset A',
      targets: [{ target_id: target.id, status: 'synced', channel_id: '99' }],
    }], upstreamB.id)

    expect(applied).toBe(false)
    expect(store.matrix).toEqual(original)
  })

  it('does not let a stale A refresh overwrite the newer A matrix after switching A to B to A', async () => {
    const store = useConsoleStore()
    store.config = { ...sanitizedConfig, upstreams: [upstream, upstreamB] }
    store.selectedUpstreamId = upstream.id
    store.matrix = emptyMatrix
    store.matrixState = 'ready'

    let finishRefresh: ((value: { refreshed: boolean }) => void) | undefined
    const pendingRefresh = new Promise<{ refreshed: boolean }>((resolve) => { finishRefresh = resolve })
    vi.spyOn(api, 'refreshUpstream').mockReturnValue(pendingRefresh)
    vi.spyOn(api, 'getMatrix').mockImplementation(async (upstreamId) => ({
      ...emptyMatrix,
      upstream_id: upstreamId,
      targets: [{ ...target, name: upstreamId === upstream.id ? 'Fresh A target' : 'Source B target' }],
    }))

    const staleRefresh = store.refreshAssets()
    await store.loadMatrix(upstreamB.id)
    await store.loadMatrix(upstream.id)
    finishRefresh?.({ refreshed: true })
    await staleRefresh

    expect(store.selectedUpstreamId).toBe(upstream.id)
    expect(store.matrix?.targets[0]?.name).toBe('Fresh A target')
    expect(store.matrixState).toBe('ready')
    expect(vi.mocked(api.getMatrix).mock.calls.map(([upstreamId]) => upstreamId)).toEqual([
      upstreamB.id,
      upstream.id,
    ])
  })

  it('refreshes matrix columns after target changes and loads the next source after deletion', async () => {
    const store = useConsoleStore()
    const targetB = { ...target, id: 'target-b', name: 'Target B' }
    store.config = {
      ...sanitizedConfig,
      targets: [target],
      upstreams: [upstream, upstreamB],
    }
    store.selectedUpstreamId = upstream.id
    store.matrix = emptyMatrix

    vi.spyOn(api, 'getMatrix')
      .mockResolvedValueOnce({ ...emptyMatrix, targets: [target, targetB] })
      .mockResolvedValueOnce({ ...emptyMatrix, targets: [targetB] })
      .mockResolvedValueOnce({ ...emptyMatrix, upstream_id: upstreamB.id, targets: [targetB] })

    await store.upsertTarget(targetB)
    expect(store.matrix?.targets.map((item) => item.id)).toEqual(['target-a', 'target-b'])

    await store.removeTarget(target.id)
    expect(store.matrix?.targets.map((item) => item.id)).toEqual(['target-b'])

    await store.removeUpstream(upstream.id)
    expect(store.selectedUpstreamId).toBe(upstreamB.id)
    expect(store.matrix?.upstream_id).toBe(upstreamB.id)
    expect(api.getMatrix).toHaveBeenCalledTimes(3)
  })

  it('updates only desensitized resources and matrix results', async () => {
    const store = useConsoleStore()
    store.config = { ...sanitizedConfig, targets: [...sanitizedConfig.targets], upstreams: [...sanitizedConfig.upstreams] }
    store.selectedUpstreamId = upstream.id
    store.matrixState = 'ready'
    store.matrix = {
      ...emptyMatrix,
      rows: [
        {
          asset: {
            id: 'asset-a',
            source_id: 'source-a',
            source_type: 'newapi',
            provider: 'openai',
            raw_type: 'OpenAI',
            kind: 'static_api_key',
            name: 'Asset A',
            base_url: '',
            models: ['model-a'],
            enabled: true,
            secret_readable: true,
            metadata: {},
          },
          cells: [
            {
              target_id: 'target-a',
              status: 'drifted',
              channel_id: '42',
              differences: [{ field: 'weight', expected: 100, actual: 80 }],
            },
          ],
        },
      ],
    }
    vi.spyOn(api, 'getMatrix').mockImplementation(async () => store.matrix as MatrixData)

    store.updateCell('asset-a', { target_id: 'target-a', status: 'failed', retryable: true })
    expect(store.matrix.rows[0]?.cells[0]).toMatchObject({
      status: 'drifted',
      differences: [{ field: 'weight', expected: 100, actual: 80 }],
    })
    store.markDriftAccepted('asset-a', 'target-a')
    expect(store.driftItems).toEqual([])
    store.updateCell('asset-a', { target_id: 'target-a', status: 'needs_reconcile', retryable: true })
    expect(store.matrix.rows[0]?.cells[0]?.status).toBe('needs_reconcile')

    await store.upsertTarget({ ...target, name: 'Updated target' })
    await store.upsertTarget({ ...target, id: 'target-b', name: 'Target B' })
    expect(store.targetName('target-a')).toBe('Updated target')
    expect(store.targetName('missing')).toBe('missing')
    store.selectedTargetId = 'target-a'
    await store.removeTarget('target-a')
    expect(store.selectedTargetId).toBe('target-b')

    await store.upsertUpstream({ ...upstream, name: 'Updated source' })
    await store.upsertUpstream({ ...upstream, id: 'source-b', name: 'Source B' })
    store.selectedUpstreamId = 'source-a'
    await store.removeUpstream('source-a')
    expect(store.selectedUpstreamId).toBe('source-b')

    store.channels = [channel]
    store.replaceChannel({ ...channel, name: 'Updated channel' })
    expect(store.channels[0]?.name).toBe('Updated channel')
    store.removeChannel('42')
    expect(store.channels).toEqual([])

    store.replaceAppSettings({ ...settings, sync_concurrency: 6 })
    expect(store.config.app.sync_concurrency).toBe(6)
  })
})
