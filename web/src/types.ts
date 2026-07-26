export type ViewName = 'matrix' | 'channels' | 'drift' | 'settings'

export type PlatformType = 'newapi' | 'cliproxyapi' | 'sub2api'

export interface AppSettings {
  host: string
  port: number
  reconcile_interval: string
  request_timeout: string
  sync_concurrency: number
}

export interface TargetConfig {
  id: string
  name: string
  type: Exclude<PlatformType, 'sub2api'>
  base_url: string
  user_id?: number
}

export interface UpstreamConfig {
  id: string
  name: string
  type: PlatformType
  base_url: string
  user_id?: number
  sync_mappings?: unknown[]
}

export interface SanitizedConfig {
  app: AppSettings
  targets: TargetConfig[]
  upstreams: UpstreamConfig[]
}

export type AssetKind = 'static_api_key' | 'oauth_auth_file' | 'proxy_endpoint_key'

export interface UpstreamAsset {
  id: string
  source_id: string
  source_type: string
  provider: string
  raw_type: string
  kind: AssetKind
  name: string
  base_url: string
  models: string[]
  enabled: boolean
  secret_readable: boolean
  metadata: Record<string, string>
}

export type MatrixStatus =
  | 'unsynced'
  | 'synced'
  | 'drifted'
  | 'incompatible'
  | 'needs_reconcile'

export type SyncStatus = 'synced' | 'incompatible' | 'needs_reconcile' | 'failed'

export interface DriftDifference {
  field: string
  expected: unknown
  actual: unknown
}

export interface MatrixCell {
  target_id: string
  status: MatrixStatus
  channel_id?: string
  code?: string
  retryable?: boolean
  differences?: DriftDifference[]
}

export interface MatrixRow {
  asset: UpstreamAsset
  cells: MatrixCell[]
}

export interface MatrixData {
  upstream_id: string
  refreshed: boolean
  targets: TargetConfig[]
  rows: MatrixRow[]
}

export interface Channel {
  id: string
  name: string
  provider: string
  raw_type: string
  base_url: string
  models: string[]
  group: string
  priority: number
  weight: number
  enabled: boolean
  managed: boolean
  upstream_asset_id?: string
}

export interface ChannelInput {
  name: string
  base_url: string
  models: string[]
  group: string
  priority: number
  weight: number
  enabled: boolean
}

export interface SyncSettings {
  models: string[]
  group: string
  priority: number
  weight: number
}

export interface SyncTargetResult {
  target_id: string
  status: SyncStatus
  channel_id?: string
  code?: string
  retryable?: boolean
}

export interface SyncResponse {
  targets: SyncTargetResult[]
}

export interface AssetSyncResult {
  assetId: string
  assetName: string
  targets: SyncTargetResult[]
}

export interface DriftItem {
  assetId: string
  assetName: string
  targetId: string
  targetName: string
  channelId: string
  differences: DriftDifference[]
}
