export type ViewName = 'matrix' | 'channels' | 'drift' | 'settings'

export type TargetPlatformType = 'newapi' | 'cliproxyapi'
export type UpstreamPlatformType = 'newapi' | 'generic'
export type PlatformType = TargetPlatformType | UpstreamPlatformType
export type TargetValidationStatus = 'unverified' | 'verified' | 'failed'

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
  type: TargetPlatformType
  base_url: string
  user_id?: number
  validation_status?: TargetValidationStatus
  validated_at?: string
  validation_capabilities?: Record<string, unknown>
}

export interface UpstreamConfig {
  id: string
  name: string
  type: UpstreamPlatformType
  base_url: string
  keys?: UpstreamKey[]
  user_id?: number
  discovery_mode?: string
  effective_discovery_mode?: string
  mode_status?: string
  mode_error_code?: string
  manage_tokens?: boolean
  sync_mappings?: unknown[]
}

export interface UpstreamKey {
  id: string
  name: string
  enabled: boolean
  models?: string[]
  credential_present: boolean
  fingerprint?: string
  source?: string
  source_group?: string
  model_count?: number
  discovery_status?: string
  snapshot_status?: 'ready' | 'empty' | 'stale' | 'unverified'
  discovered_at?: string
}

export interface UpstreamKeyCreateInput {
  id: string
  name: string
  api_key: string
  enabled: boolean
  models: string[]
}

export interface UpstreamKeyUpdateInput {
  name?: string
  api_key?: string
  enabled?: boolean
  models?: string[]
}

export interface ConnectionTestResult {
  reachable: boolean
  authenticated: boolean
  authorized: boolean
  resource_count: number
  capabilities: Record<string, unknown>
  validation_status?: TargetValidationStatus
  validated_at?: string
}

export type ModelProbeProtocol = 'auto' | 'chat_completions' | 'responses' | 'completions'

export type ModelProbeStatus =
  | 'healthy'
  | 'reachable_inconclusive'
  | 'authentication_failed'
  | 'model_unavailable'
  | 'rate_limited'
  | 'timeout'
  | 'network_error'
  | 'invalid_response'
  | 'unsupported'

export interface ModelProbeResult {
  key_id: string
  model: string
  protocol: Exclude<ModelProbeProtocol, 'auto'>
  status: ModelProbeStatus
  latency_ms: number
  checked_at: string
  error_code: string
  retryable: boolean
  retry_after_seconds?: number
  template_version: string
}

export interface KeyModelObservation {
  id: string
  discovery_status: 'discovered' | 'unverified'
  probe?: ModelProbeResult | null
}

export interface KeyModelsResponse {
  upstream_id: string
  key_id: string
  models: KeyModelObservation[]
  snapshot_status: 'ready' | 'empty' | 'stale' | 'unverified'
  snapshot_scope: 'persisted' | 'runtime'
  discovered_at?: string
}

export interface ModelDiscoveryItem {
  key_id: string
  status: 'succeeded' | 'empty' | 'authentication_failed' | 'rate_limited' | 'unsupported' | 'failed'
  model_count: number
  discovered_at?: string
  error_code?: string
  retryable: boolean
  retry_after_seconds?: number
}

export interface ModelDiscoveryTask {
  task_id: string
  key_ids: string[]
  completed: boolean
  status: 'succeeded' | 'partially_failed' | 'failed'
  items: ModelDiscoveryItem[]
}

export type TaskHistoryStatus = 'running' | 'succeeded' | 'partially_failed' | 'failed'

export interface TaskSummary {
  total: number
  succeeded: number
  failed: number
}

export interface TaskHistoryItem {
  item_id?: string
  asset_id?: string
  target_id?: string
  scope?: string
  status?: string
  code?: string
  error_code?: string
  message?: string
  [key: string]: unknown
}

export interface TaskHistoryRecord {
  task_id: string
  type: string
  scope: string
  status: TaskHistoryStatus
  completed: boolean
  started_at: string
  completed_at?: string | null
  summary: TaskSummary
  items?: TaskHistoryItem[]
}

export interface TaskHistoryDetail extends TaskHistoryRecord {
  items: TaskHistoryItem[]
}

export interface TaskHistoryListResponse {
  tasks: TaskHistoryRecord[]
  meta: {
    total: number
    capacity: number
  }
}

export interface UpstreamGroup {
  name: string
  description?: string
  ratio: number | null
  ratio_known: boolean
  models: string[]
  model_count: number
  models_verified: boolean
  auto: boolean
}

export interface UpstreamGroupsResponse {
  upstream_id: string
  refreshed: boolean
  groups: UpstreamGroup[]
}

export interface SanitizedConfig {
  app: AppSettings
  targets: TargetConfig[]
  upstreams: UpstreamConfig[]
}

export interface RuntimeInfo {
  status: string
  version: string
  build_date: string
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
  unit_id?: string
  asset_id?: string
  target_id: string
  status: SyncStatus
  channel_id?: string
  code?: string
  retryable?: boolean
  retry_after_seconds?: number
  effective_models?: string[]
  excluded_models?: string[]
  warnings?: string[]
}

export interface SyncResponse {
  task_id?: string
  units: SyncTargetResult[]
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
