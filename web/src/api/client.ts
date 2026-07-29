import type {
  AppSettings,
  Channel,
  ChannelInput,
  MatrixData,
  RuntimeInfo,
  SanitizedConfig,
  SyncResponse,
  TargetConfig,
  UpstreamAsset,
  UpstreamConfig,
} from '@/types'

const API_PREFIX = '/api/v1'

interface SuccessEnvelope<T> {
  success: true
  data: T
  request_id: string
}

interface ErrorEnvelope {
  success: false
  error: {
    code: string
    message: string
  }
  request_id: string
}

interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE'
  body?: unknown
  signal?: AbortSignal
}

export class ApiClientError extends Error {
  readonly code: string
  readonly requestId?: string
  readonly status?: number

  constructor(code: string, message: string, options: { requestId?: string; status?: number } = {}) {
    super(message)
    this.name = 'ApiClientError'
    this.code = code
    this.requestId = options.requestId
    this.status = options.status
  }
}

function isSuccessEnvelope<T>(value: unknown): value is SuccessEnvelope<T> {
  if (!value || typeof value !== 'object') return false
  const envelope = value as Record<string, unknown>
  return envelope.success === true && 'data' in envelope && typeof envelope.request_id === 'string'
}

function isErrorEnvelope(value: unknown): value is ErrorEnvelope {
  if (!value || typeof value !== 'object') return false
  const envelope = value as Record<string, unknown>
  if (envelope.success !== false || typeof envelope.request_id !== 'string') return false
  if (!envelope.error || typeof envelope.error !== 'object') return false
  const error = envelope.error as Record<string, unknown>
  return typeof error.code === 'string' && typeof error.message === 'string'
}

function validClientPath(path: string): boolean {
  return path.startsWith('/') && !path.startsWith('//') && !path.includes('://')
}

export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  if (!validClientPath(path)) {
    throw new ApiClientError('invalid_client_path', '请求路径不属于 SyncHub 管理 API')
  }

  const headers: Record<string, string> = { Accept: 'application/json' }
  const init: RequestInit = {
    method: options.method ?? 'GET',
    headers,
    signal: options.signal,
  }
  if (options.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    init.body = JSON.stringify(options.body)
  }

  let response: Response
  try {
    response = await fetch(`${API_PREFIX}${path}`, init)
  } catch {
    throw new ApiClientError('network_error', '无法连接 SyncHub 服务')
  }

  let payload: unknown
  try {
    payload = await response.json()
  } catch {
    throw new ApiClientError('invalid_response', 'SyncHub 返回了无法识别的响应', {
      status: response.status,
    })
  }

  if (isSuccessEnvelope<T>(payload)) return payload.data
  if (isErrorEnvelope(payload)) {
    throw new ApiClientError(payload.error.code, payload.error.message, {
      requestId: payload.request_id,
      status: response.status,
    })
  }
  throw new ApiClientError('invalid_response', 'SyncHub 返回了无法识别的响应', {
    status: response.status,
  })
}

function segment(value: string): string {
  return encodeURIComponent(value)
}

export const api = {
  getHealth: (signal?: AbortSignal) => request<RuntimeInfo>('/health', { signal }),
  getConfig: (signal?: AbortSignal) => request<SanitizedConfig>('/config', { signal }),
  updateApp: (input: AppSettings) => request<AppSettings>('/config/app', { method: 'PUT', body: input }),
  createTarget: (input: Record<string, unknown>) =>
    request<TargetConfig>('/targets', { method: 'POST', body: input }),
  updateTarget: (targetId: string, input: Record<string, unknown>) =>
    request<TargetConfig>(`/targets/${segment(targetId)}`, { method: 'PUT', body: input }),
  deleteTarget: (targetId: string) =>
    request<Record<string, never>>(`/targets/${segment(targetId)}`, { method: 'DELETE' }),
  createUpstream: (input: Record<string, unknown>) =>
    request<UpstreamConfig>('/upstreams', { method: 'POST', body: input }),
  updateUpstream: (upstreamId: string, input: Record<string, unknown>) =>
    request<UpstreamConfig>(`/upstreams/${segment(upstreamId)}`, { method: 'PUT', body: input }),
  deleteUpstream: (upstreamId: string) =>
    request<Record<string, never>>(`/upstreams/${segment(upstreamId)}`, { method: 'DELETE' }),
  getChannels: (targetId: string, signal?: AbortSignal) =>
    request<{ channels: Channel[] }>(`/targets/${segment(targetId)}/channels`, { signal }),
  updateChannel: (targetId: string, channelId: string, input: ChannelInput) =>
    request<Channel>(`/targets/${segment(targetId)}/channels/${segment(channelId)}`, {
      method: 'PUT',
      body: input,
    }),
  deleteChannel: (targetId: string, channelId: string) =>
    request<Record<string, never>>(`/targets/${segment(targetId)}/channels/${segment(channelId)}`, {
      method: 'DELETE',
    }),
  refreshUpstream: (upstreamId: string) =>
    request<{ refreshed: boolean }>(`/upstreams/${segment(upstreamId)}/refresh`, { method: 'POST' }),
  getAssets: (upstreamId: string) =>
    request<{ assets: UpstreamAsset[]; refreshed: boolean }>(`/upstreams/${segment(upstreamId)}/assets`),
  getMatrix: (upstreamId: string, signal?: AbortSignal) =>
    request<MatrixData>(`/matrix?upstream_id=${encodeURIComponent(upstreamId)}`, { signal }),
  sync: (input: Record<string, unknown>) => request<SyncResponse>('/sync', { method: 'POST', body: input }),
  reconcile: (targetId: string) =>
    request<Record<string, unknown>>(`/targets/${segment(targetId)}/reconcile`, { method: 'POST' }),
  acceptDrift: (targetId: string, input: { upstream_asset_id: string; channel_id: string }) =>
    request<Record<string, unknown>>(`/targets/${segment(targetId)}/drift/accept`, {
      method: 'POST',
      body: input,
    }),
}

export function safeErrorMessage(error: unknown): string {
  return error instanceof ApiClientError ? error.message : '操作未完成，请重试'
}
