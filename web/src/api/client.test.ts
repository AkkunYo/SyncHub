import { describe, expect, it, vi } from 'vitest'

import { ApiClientError, api, request } from './client'

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

describe('SyncHub API client', () => {
  it('only calls the versioned SyncHub API and unwraps a success envelope', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({
        success: true,
        data: { targets: [], upstreams: [] },
        request_id: 'req-config',
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    await expect(api.getConfig()).resolves.toEqual({ targets: [], upstreams: [] })
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/config',
      expect.objectContaining({ headers: expect.objectContaining({ Accept: 'application/json' }) }),
    )
  })

  it('sends JSON writes without leaking fields outside the supplied body', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({ success: true, data: { id: 'target-a' }, request_id: 'req-create' }),
    )
    vi.stubGlobal('fetch', fetchMock)

    await request('/targets', {
      method: 'POST',
      body: { id: 'target-a', name: 'Primary', base_url: 'https://target.invalid' },
    })

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/targets',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ id: 'target-a', name: 'Primary', base_url: 'https://target.invalid' }),
        headers: expect.objectContaining({ 'Content-Type': 'application/json' }),
      }),
    )
  })

  it('exposes only the stable API error and request id', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(
          {
            success: false,
            error: { code: 'upstream_timeout', message: '上游请求超时' },
            request_id: 'req-timeout',
          },
          504,
        ),
      ),
    )

    const error = await request('/config').catch((reason: unknown) => reason)

    expect(error).toBeInstanceOf(ApiClientError)
    expect(error).toMatchObject({ code: 'upstream_timeout', message: '上游请求超时', requestId: 'req-timeout' })
  })

  it('replaces network and malformed responses with safe client errors', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValueOnce(new Error('private transport detail')))

    await expect(request('/config')).rejects.toMatchObject({
      code: 'network_error',
      message: '无法连接 SyncHub 服务',
    })

    vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(jsonResponse({ unexpected: true })))

    await expect(request('/config')).rejects.toMatchObject({
      code: 'invalid_response',
      message: 'SyncHub 返回了无法识别的响应',
    })
  })

  it.each([
    new Response('not-json', { status: 502, headers: { 'Content-Type': 'application/json' } }),
    jsonResponse({ success: false, error: null, request_id: 'req-bad-error' }, 502),
    jsonResponse({ success: false, error: { code: 42, message: null }, request_id: 'req-bad-fields' }, 502),
    jsonResponse({ success: true, request_id: 'req-missing-data' }),
  ])('rejects malformed API envelopes without exposing their content', async (response) => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response))

    await expect(request('/config')).rejects.toMatchObject({
      code: 'invalid_response',
      message: 'SyncHub 返回了无法识别的响应',
    })
  })

  it('rejects paths outside the contract prefix before calling fetch', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)

    await expect(request('https://target.invalid/api/channel')).rejects.toMatchObject({
      code: 'invalid_client_path',
    })
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('maps every console operation to its documented management route', async () => {
    const fetchMock = vi.fn().mockImplementation(() =>
      Promise.resolve(
        jsonResponse({ success: true, data: { channels: [], assets: [], targets: [] }, request_id: 'req-route' }),
      ),
    )
    vi.stubGlobal('fetch', fetchMock)

    await api.updateApp({
      host: '127.0.0.1',
      port: 8888,
      reconcile_interval: '5m0s',
      request_timeout: '15s',
      sync_concurrency: 4,
    })
    await api.createTarget({ id: 'target-a' })
    await api.updateTarget('target/a', { name: 'Target' })
    await api.deleteTarget('target/a')
    await api.createUpstream({ id: 'source-a' })
    await api.updateUpstream('source/a', { name: 'Source' })
    await api.deleteUpstream('source/a')
    await api.testTargetConnection('target/a')
    await api.testUpstreamConnection('source/a')
    await api.getUpstreamKeys('source/a')
    await api.createUpstreamKey('source/a', {
      id: 'key/a',
      name: 'Primary',
      api_key: 'write-only',
      enabled: true,
      models: [],
    })
    await api.updateUpstreamKey('source/a', 'key/a', { name: 'Renamed', enabled: false })
    await api.deleteUpstreamKey('source/a', 'key/a')
    await api.getChannels('target/a')
    await api.updateChannel('target/a', 'channel/1', {
      name: 'Channel',
      base_url: '',
      models: ['model-a'],
      group: 'default',
      priority: 0,
      weight: 100,
      enabled: true,
    })
    await api.deleteChannel('target/a', 'channel/1')
    await api.refreshUpstream('source/a')
    await api.getAssets('source/a')
    await api.getMatrix('source/a')
    await api.sync({ asset_id: 'asset-a' })
    await api.reconcile('target/a')
    await api.acceptDrift('target/a', { upstream_asset_id: 'asset-a', channel_id: 'channel/1' })

    const routes = fetchMock.mock.calls.map(([input, options]) => `${options.method ?? 'GET'} ${String(input)}`)
    expect(routes).toEqual([
      'PUT /api/v1/config/app',
      'POST /api/v1/targets',
      'PUT /api/v1/targets/target%2Fa',
      'DELETE /api/v1/targets/target%2Fa',
      'POST /api/v1/upstreams',
      'PUT /api/v1/upstreams/source%2Fa',
      'DELETE /api/v1/upstreams/source%2Fa',
      'POST /api/v1/targets/target%2Fa/connection-tests',
      'POST /api/v1/upstreams/source%2Fa/connection-tests',
      'GET /api/v1/upstreams/source%2Fa/keys',
      'POST /api/v1/upstreams/source%2Fa/keys',
      'PATCH /api/v1/upstreams/source%2Fa/keys/key%2Fa',
      'DELETE /api/v1/upstreams/source%2Fa/keys/key%2Fa',
      'GET /api/v1/targets/target%2Fa/channels',
      'PUT /api/v1/targets/target%2Fa/channels/channel%2F1',
      'DELETE /api/v1/targets/target%2Fa/channels/channel%2F1',
      'POST /api/v1/upstreams/source%2Fa/refresh',
      'GET /api/v1/upstreams/source%2Fa/assets',
      'GET /api/v1/matrix?upstream_id=source%2Fa',
      'POST /api/v1/sync',
      'POST /api/v1/targets/target%2Fa/reconcile',
      'POST /api/v1/targets/target%2Fa/drift/accept',
    ])
  })
})
