<script setup lang="ts">
import { computed, inject, reactive, ref, watch } from 'vue'
import {
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Pencil,
  RefreshCw,
  RotateCcw,
  Search,
  Trash2,
} from 'lucide-vue-next'
import { RouterLink, routeLocationKey, routerKey } from 'vue-router'

import { api, safeErrorMessage } from '@/api/client'
import ModalDialog from '@/components/ModalDialog.vue'
import TableSkeleton from '@/components/TableSkeleton.vue'
import { useConsoleStore } from '@/stores/console'
import type { Channel, ChannelInput } from '@/types'

const store = useConsoleStore()
const route = inject(routeLocationKey, null)
const router = inject(routerKey, null)
const pageSize = 10
const fallbackPage = ref(1)
const fallbackSearch = ref('')
const fallbackSource = ref<'all' | 'managed' | 'native'>('all')
const fallbackStatus = ref<'all' | 'enabled' | 'disabled'>('all')
const editChannel = ref<Channel | null>(null)
const deleteChannel = ref<Channel | null>(null)
const actionError = ref('')
const saving = ref(false)
const deleting = ref(false)
function queryValue(key: string): string {
  const value = route?.query[key]
  return Array.isArray(value) ? value[0] ?? '' : value ?? ''
}

fallbackSearch.value = queryValue('q')
fallbackSource.value = queryValue('source') === 'managed' || queryValue('source') === 'native'
  ? queryValue('source') as 'managed' | 'native'
  : 'all'
fallbackStatus.value = queryValue('status') === 'enabled' || queryValue('status') === 'disabled'
  ? queryValue('status') as 'enabled' | 'disabled'
  : 'all'
const initialPage = Number.parseInt(queryValue('page'), 10)
fallbackPage.value = Number.isFinite(initialPage) && initialPage > 0 ? initialPage : 1

function updateQuery(changes: Record<string, string | undefined>): void {
  const url = new URL(window.location.href)
  for (const [key, value] of Object.entries(changes)) {
    if (value) url.searchParams.set(key, value)
    else url.searchParams.delete(key)
  }
  window.history.replaceState(window.history.state, '', `${url.pathname}${url.search}${url.hash}`)
}

const searchQuery = computed({
  get: () => fallbackSearch.value,
  set: (value: string) => {
    fallbackSearch.value = value
    updateQuery({ q: value.trim() || undefined, page: undefined })
  },
})
const sourceFilter = computed<'all' | 'managed' | 'native'>({
  get: () => {
    const value = fallbackSource.value
    return value === 'managed' || value === 'native' ? value : 'all'
  },
  set: (value) => {
    fallbackSource.value = value
    updateQuery({ source: value === 'all' ? undefined : value, page: undefined })
  },
})
const statusFilter = computed<'all' | 'enabled' | 'disabled'>({
  get: () => {
    const value = fallbackStatus.value
    return value === 'enabled' || value === 'disabled' ? value : 'all'
  },
  set: (value) => {
    fallbackStatus.value = value
    updateQuery({ status: value === 'all' ? undefined : value, page: undefined })
  },
})
const currentPage = computed({
  get: () => {
    const page = fallbackPage.value
    return Number.isFinite(page) && page > 0 ? page : 1
  },
  set: (value: number) => {
    const page = Math.max(1, Math.floor(value))
    fallbackPage.value = page
    updateQuery({ page: page === 1 ? undefined : String(page) })
  },
})
const form = reactive<ChannelInput>({
  name: '',
  base_url: '',
  models: [],
  group: 'default',
  priority: 0,
  weight: 100,
  enabled: true,
})
const modelText = ref('')

const filteredChannels = computed(() => {
  const query = searchQuery.value.trim().toLocaleLowerCase()

  return store.channels.filter((channel) => {
    const matchesQuery = !query || [
      channel.name,
      channel.id,
      channel.provider,
      ...channel.models,
      channel.group,
    ].some((value) => String(value).toLocaleLowerCase().includes(query))
    const matchesSource = sourceFilter.value === 'all'
      || (sourceFilter.value === 'managed' ? channel.managed : !channel.managed)
    const matchesStatus = statusFilter.value === 'all'
      || (statusFilter.value === 'enabled' ? channel.enabled : !channel.enabled)

    return matchesQuery && matchesSource && matchesStatus
  })
})
const pageCount = computed(() => Math.max(1, Math.ceil(filteredChannels.value.length / pageSize)))
const safePage = computed(() => Math.min(currentPage.value, pageCount.value))
const visibleChannels = computed(() => {
  const start = (safePage.value - 1) * pageSize
  return filteredChannels.value.slice(start, start + pageSize)
})
const rangeStart = computed(() => (filteredChannels.value.length ? (safePage.value - 1) * pageSize + 1 : 0))
const rangeEnd = computed(() => Math.min(safePage.value * pageSize, filteredChannels.value.length))
const managedChannelCount = computed(() => store.channels.filter((channel) => channel.managed).length)
const nativeChannelCount = computed(() => store.channels.length - managedChannelCount.value)
const enabledChannelCount = computed(() => store.channels.filter((channel) => channel.enabled).length)
const channelSummaryReady = computed(() => store.channelState === 'ready')

function clearFilters(): void {
  fallbackSearch.value = ''
  fallbackSource.value = 'all'
  fallbackStatus.value = 'all'
  fallbackPage.value = 1
  updateQuery({ q: undefined, source: undefined, status: undefined, page: undefined })
}

function onTargetChange(event: Event): void {
  const targetId = (event.target as HTMLSelectElement).value
  if (router) {
    void router.push({ name: 'target-channels', params: { id: targetId }, query: {} })
  }
  void store.loadChannels(targetId)
}

function goToPage(page: number): void {
  currentPage.value = Math.max(1, Math.min(page, pageCount.value))
}

watch([pageCount, currentPage], ([count, page]) => {
  if (page > count) currentPage.value = count
})

function openEdit(channel: Channel): void {
  editChannel.value = channel
  form.name = channel.name
  form.base_url = channel.base_url
  form.models = [...channel.models]
  form.group = channel.group
  form.priority = channel.priority
  form.weight = channel.weight
  form.enabled = channel.enabled
  modelText.value = channel.models.join(', ')
  actionError.value = ''
}

function closeEdit(): void {
  if (saving.value) return
  editChannel.value = null
}

function closeDelete(): void {
  if (deleting.value) return
  deleteChannel.value = null
}

function validOptionalUrl(value: string): boolean {
  if (!value) return true
  try {
    const url = new URL(value)
    return url.protocol === 'http:' || url.protocol === 'https:'
  } catch {
    return false
  }
}

async function saveChannel(): Promise<void> {
  if (!editChannel.value || saving.value) return
  const targetId = store.selectedTargetId
  const editing = editChannel.value
  if (!form.name.trim()) {
    actionError.value = '渠道名称不能为空'
    return
  }
  if (!validOptionalUrl(form.base_url.trim())) {
    actionError.value = '请输入绝对 HTTP(S) 地址'
    return
  }
  const models = [...new Set(modelText.value.split(',').map((model) => model.trim()).filter(Boolean))]
  if (models.length === 0) {
    actionError.value = '至少填写一个模型'
    return
  }

  saving.value = true
  actionError.value = ''
  try {
    const channel = await api.updateChannel(targetId, editing.id, {
      name: form.name.trim(),
      base_url: form.base_url.trim(),
      models,
      group: form.group.trim() || 'default',
      priority: form.priority,
      weight: form.weight,
      enabled: form.enabled,
    })
    if (store.selectedTargetId === targetId) store.replaceChannel(channel)
    if (editChannel.value === editing) editChannel.value = null
  } catch (error) {
    if (editChannel.value === editing) actionError.value = safeErrorMessage(error)
  } finally {
    saving.value = false
  }
}

async function confirmDelete(): Promise<void> {
  if (!deleteChannel.value || deleting.value) return
  const targetId = store.selectedTargetId
  const deleted = deleteChannel.value
  deleting.value = true
  actionError.value = ''
  try {
    await api.deleteChannel(targetId, deleted.id)
    if (deleted.managed && deleted.upstream_asset_id) {
      store.markChannelDeleted(deleted.upstream_asset_id, targetId, deleted.id)
    }
    if (store.selectedTargetId === targetId) store.removeChannel(deleted.id)
    if (deleteChannel.value === deleted) deleteChannel.value = null
  } catch (error) {
    if (deleteChannel.value === deleted) actionError.value = safeErrorMessage(error)
  } finally {
    deleting.value = false
  }
}
</script>

<template>
  <section class="page" aria-labelledby="channels-heading">
    <header class="page-header">
      <div>
        <h1 id="channels-heading">目标渠道</h1>
      </div>
    </header>

    <div v-if="store.targets.length === 0" class="state-panel">
      <h2>尚未配置目标实例</h2>
      <RouterLink class="primary-button" to="/targets">管理目标实例</RouterLink>
    </div>

    <template v-else>
      <section class="channel-summary" aria-label="渠道概览">
        <div>
          <strong>{{ channelSummaryReady ? `${store.channels.length} 个渠道` : '--' }}</strong><span>全部</span>
        </div>
        <div>
          <strong>{{ channelSummaryReady ? `${managedChannelCount} 个托管` : '--' }}</strong><span>受管资源</span>
        </div>
        <div>
          <strong>{{ channelSummaryReady ? `${nativeChannelCount} 个原生` : '--' }}</strong><span>目标平台</span>
        </div>
        <div>
          <strong>{{ channelSummaryReady ? `${enabledChannelCount} 个启用` : '--' }}</strong><span>当前状态</span>
        </div>
      </section>

      <section class="workspace-panel channels-workspace" aria-label="渠道列表">
        <div class="workspace-toolbar">
          <label class="filter-field target-filter">
            <span>目标实例</span>
            <select :value="store.selectedTargetId" @change="onTargetChange">
              <option v-for="target in store.targets" :key="target.id" :value="target.id">
                {{ target.name }}
              </option>
            </select>
          </label>

          <div class="toolbar-filters">
            <label class="search-field">
              <span class="sr-only">搜索渠道</span>
              <Search :size="16" aria-hidden="true" />
              <input
                v-model="searchQuery"
                type="search"
                aria-label="搜索渠道"
                placeholder="搜索名称、ID、模型"
                autocomplete="off"
              />
            </label>
            <label class="filter-field">
              <span>来源</span>
              <select v-model="sourceFilter" aria-label="来源">
                <option value="all">全部来源</option>
                <option value="managed">托管来源</option>
                <option value="native">目标原生</option>
              </select>
            </label>
            <label class="filter-field">
              <span>状态</span>
              <select v-model="statusFilter" aria-label="状态">
                <option value="all">全部状态</option>
                <option value="enabled">已启用</option>
                <option value="disabled">已停用</option>
              </select>
            </label>
            <button
              class="icon-button channel-refresh"
              type="button"
              aria-label="刷新目标渠道"
              title="刷新目标渠道"
              :disabled="!store.selectedTargetId || store.channelState === 'loading'"
              @click="store.loadChannels()"
            >
              <RefreshCw :size="18" aria-hidden="true" />
            </button>
          </div>
        </div>

        <TableSkeleton
          v-if="store.channelState === 'loading'"
          label="正在实时读取完整渠道列表"
          :columns="8"
        />

        <div v-else-if="store.channelState === 'error'" class="state-panel state-error" role="alert">
          <p>{{ store.channelError }}</p>
          <button class="secondary-button" type="button" @click="store.loadChannels()">
            <RotateCcw :size="16" aria-hidden="true" />
            重试
          </button>
        </div>

        <div v-else-if="store.channels.length === 0" class="state-panel">
          <h2>目标实例没有渠道</h2>
          <p>该列表来自目标平台实时 API。</p>
        </div>

        <template v-else>
          <p class="table-result-count" aria-live="polite">
            显示 {{ rangeStart }}-{{ rangeEnd }} / {{ filteredChannels.length }} 个渠道
          </p>

          <div v-if="filteredChannels.length === 0" class="state-panel filter-empty">
            <h2>没有匹配的渠道</h2>
            <p>调整搜索词或筛选条件后重试。</p>
            <button class="secondary-button" type="button" @click="clearFilters">清除筛选</button>
          </div>

          <div v-else class="table-scroll channels-table-scroll">
            <table class="data-table channels-table">
              <thead>
                <tr>
                  <th>渠道</th>
                  <th>来源</th>
                  <th>模型</th>
                  <th>分组</th>
                  <th>优先级</th>
                  <th>权重</th>
                  <th>状态</th>
                  <th class="actions-cell">操作</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="channel in visibleChannels" :key="channel.id">
                  <td class="channel-cell" data-label="渠道">
                    <strong>{{ channel.name }}</strong>
                    <small>{{ channel.provider }} / #{{ channel.id }}</small>
                  </td>
                  <td class="source-cell" data-label="来源">
                    <span class="origin-badge" :class="{ managed: channel.managed }">
                      {{ channel.managed ? 'SyncHub 管理' : '原生渠道' }}
                    </span>
                    <small v-if="channel.upstream_asset_id">{{ channel.upstream_asset_id }}</small>
                  </td>
                  <td class="models-cell" data-label="模型">
                    <span class="model-list" :title="channel.models.join(', ')">{{ channel.models.join(', ') }}</span>
                    <details class="mobile-model-details">
                      <summary>
                        <span class="mobile-model-summary-text">{{ channel.models.join(', ') || '--' }}</span>
                        <span class="mobile-model-count">{{ channel.models.length }} 个</span>
                        <ChevronDown :size="15" aria-hidden="true" />
                      </summary>
                      <p>{{ channel.models.join(', ') || '--' }}</p>
                    </details>
                  </td>
                  <td class="group-cell" data-label="分组">{{ channel.group }}</td>
                  <td class="priority-cell" data-label="优先级">{{ channel.priority }}</td>
                  <td class="weight-cell" data-label="权重">{{ channel.weight }}</td>
                  <td class="status-cell" data-label="状态">
                    <span
                      class="status-badge channel-state-badge"
                      :class="channel.enabled ? 'status-synced' : 'status-unsynced'"
                    >
                      {{ channel.enabled ? '启用' : '停用' }}
                    </span>
                  </td>
                  <td class="actions-cell" data-label="操作">
                    <button
                      class="icon-button icon-button-small"
                      type="button"
                      :aria-label="`编辑渠道 ${channel.name}`"
                      title="编辑渠道"
                      @click="openEdit(channel)"
                    >
                      <Pencil :size="16" aria-hidden="true" />
                      <span class="channel-action-label">编辑</span>
                    </button>
                    <button
                      class="icon-button icon-button-small danger-icon"
                      type="button"
                      :aria-label="`删除渠道 ${channel.name}`"
                      title="删除渠道"
                      @click="deleteChannel = channel; actionError = ''"
                    >
                      <Trash2 :size="16" aria-hidden="true" />
                      <span class="channel-action-label">删除</span>
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>

          <nav v-if="filteredChannels.length" class="table-pagination" aria-label="渠道分页">
            <span>第 {{ safePage }} / {{ pageCount }} 页</span>
            <div>
              <button
                class="icon-button icon-button-small"
                type="button"
                aria-label="上一页"
                title="上一页"
                :disabled="safePage <= 1"
                @click="goToPage(safePage - 1)"
              >
                <ChevronLeft :size="16" aria-hidden="true" />
              </button>
              <button
                class="icon-button icon-button-small"
                type="button"
                aria-label="下一页"
                title="下一页"
                :disabled="safePage >= pageCount"
                @click="goToPage(safePage + 1)"
              >
                <ChevronRight :size="16" aria-hidden="true" />
              </button>
            </div>
          </nav>
        </template>
      </section>
    </template>

    <ModalDialog v-if="editChannel" title="编辑目标渠道" close-label="关闭渠道编辑" @close="closeEdit">
      <form class="form-stack" @submit.prevent="saveChannel">
        <div class="form-grid">
          <label class="field field-wide">
            <span>名称</span>
            <input v-model="form.name" type="text" autocomplete="off" />
          </label>
          <label class="field field-wide">
            <span>Base URL</span>
            <input v-model="form.base_url" type="url" autocomplete="off" />
          </label>
          <label class="field field-wide">
            <span>模型</span>
            <input v-model="modelText" type="text" autocomplete="off" />
          </label>
          <label class="field">
            <span>分组</span>
            <input v-model="form.group" type="text" autocomplete="off" />
          </label>
          <label class="field">
            <span>优先级</span>
            <input v-model.number="form.priority" type="number" step="1" />
          </label>
          <label class="field">
            <span>权重</span>
            <input v-model.number="form.weight" type="number" min="0" step="1" />
          </label>
        </div>
        <label class="check-row">
          <input v-model="form.enabled" type="checkbox" />
          <span>启用渠道</span>
        </label>
        <p v-if="actionError" class="form-error" role="alert">{{ actionError }}</p>
        <footer class="form-actions">
          <button class="secondary-button" type="button" :disabled="saving" @click="closeEdit">取消</button>
          <button class="primary-button" type="submit" :disabled="saving">{{ saving ? '保存中' : '保存渠道' }}</button>
        </footer>
      </form>
    </ModalDialog>

    <ModalDialog v-if="deleteChannel" title="删除目标渠道" close-label="关闭删除确认" @close="closeDelete">
      <p>确定删除“{{ deleteChannel.name }}”吗？目标端成功删除后，其同步映射也会移除。</p>
      <p v-if="actionError" class="form-error" role="alert">{{ actionError }}</p>
      <footer class="form-actions">
        <button class="secondary-button" type="button" :disabled="deleting" @click="closeDelete">取消</button>
        <button class="danger-button" type="button" :disabled="deleting" @click="confirmDelete">
          {{ deleting ? '删除中' : '确认删除' }}
        </button>
      </footer>
    </ModalDialog>
  </section>
</template>

<style scoped>
.channel-summary {
  display: grid;
  margin-bottom: 10px;
  border-top: 1px solid var(--line);
  border-bottom: 1px solid var(--line);
  background: var(--surface);
  grid-template-columns: repeat(4, minmax(0, 1fr));
}

.channel-summary > div {
  display: grid;
  min-height: 48px;
  align-content: center;
  gap: 2px;
  padding: 7px 12px;
  border-right: 1px solid var(--line);
}

.channel-summary > div:last-child {
  border-right: 0;
}

.channel-summary strong {
  font-size: 13px;
  font-variant-numeric: tabular-nums;
}

.channel-summary span {
  color: var(--muted);
  font-size: 10px;
}

.channels-workspace {
  box-shadow: none;
}

.channels-workspace :deep(.workspace-toolbar) {
  min-height: 54px;
  padding: 8px 10px;
  background: var(--surface);
}

.channels-workspace :deep(input),
.channels-workspace :deep(select) {
  font-size: 13px;
}

.table-pagination {
  display: flex;
  min-height: 52px;
  align-items: center;
  justify-content: space-between;
  padding: 8px 10px;
  border-top: 1px solid var(--line);
  color: var(--muted);
  font-size: 12px;
}

.table-pagination > div {
  display: flex;
  gap: 6px;
}

.channels-table tbody tr {
  transition: background-color 120ms ease;
}

.channels-table :deep(.origin-badge),
.channels-table :deep(.status-badge) {
  border-radius: 4px;
}

.channel-action-label {
  display: none;
}

@media (max-width: 620px) {
  .channel-summary {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .channel-summary > div:nth-child(even) {
    border-right: 0;
  }

  .channel-summary > div:nth-child(-n + 2) {
    border-bottom: 1px solid var(--line);
  }

  .channels-workspace :deep(.workspace-toolbar) {
    grid-template-columns: minmax(0, 1fr);
    padding: 8px;
  }

  .toolbar-filters {
    display: grid;
    width: 100%;
    min-width: 0;
    grid-template-columns: repeat(2, minmax(0, 1fr)) var(--touch-target);
    gap: 8px;
  }

  .toolbar-filters .search-field {
    grid-column: 1 / -1;
  }

  .channels-workspace :deep(.target-filter),
  .toolbar-filters .search-field,
  .toolbar-filters .filter-field {
    width: 100%;
    min-width: 0;
  }

  .channel-refresh {
    width: var(--touch-target);
    min-width: var(--touch-target);
    min-height: var(--touch-target);
    align-self: end;
  }

  .channels-table tbody tr {
    display: grid;
    padding: 0 10px;
    border-top: 1px solid var(--line);
    border-left: 0;
    border-radius: 0;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 0 8px;
  }

  .channels-table td {
    min-width: 0;
    overflow-wrap: anywhere;
  }

  .channels-table .channel-cell {
    padding: 10px 0 9px;
    border-bottom: 1px solid var(--line);
  }

  .channels-table .source-cell {
    display: grid;
    padding: 9px 0;
    border-bottom: 1px solid var(--line);
    grid-column: 1 / -1;
    grid-row: 2;
    gap: 5px;
  }

  .channels-table .channel-cell small,
  .channels-table .source-cell small {
    max-width: 100%;
    overflow: visible;
    text-overflow: clip;
    white-space: normal;
    overflow-wrap: anywhere;
  }

  .channels-table .channel-cell::before,
  .channels-table .source-cell::before,
  .channels-table .status-cell::before {
    display: block;
  }

  .channels-table .models-cell {
    padding: 9px 8px;
  }

  .mobile-model-details p {
    overflow-wrap: anywhere;
  }

  .channels-table .group-cell,
  .channels-table .priority-cell,
  .channels-table .weight-cell {
    min-width: 0;
    padding: 9px 0;
    overflow-wrap: anywhere;
  }

  .channels-table .status-cell {
    display: grid;
    padding: 9px 0;
    border-top: 1px solid var(--line);
    grid-column: 1 / -1;
    grid-row: 5;
    grid-template-columns: 72px minmax(0, 1fr);
    align-items: center;
    justify-self: stretch;
    text-align: left !important;
  }

  .channels-table .status-cell::before {
    margin-bottom: 0;
  }

  .channels-table .actions-cell {
    position: static;
    display: flex;
    width: 100% !important;
    min-height: 0;
    padding: 9px 0 10px;
    border-top: 1px solid var(--line);
    grid-column: 1 / -1;
    grid-row: 6;
    gap: 8px;
    justify-content: stretch;
  }

  .channels-table .actions-cell .icon-button {
    width: auto;
    min-width: 0;
    height: var(--touch-target);
    flex: 1 1 0;
    gap: 6px;
    border-radius: 6px;
    padding: 0 10px;
  }

  .channel-action-label {
    display: inline;
  }

  .channels-workspace :deep(input),
  .channels-workspace :deep(select) {
    font-size: 16px;
  }
}
</style>
