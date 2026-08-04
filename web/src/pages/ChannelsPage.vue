<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { Pencil, RefreshCw, RotateCcw, Search, Trash2 } from 'lucide-vue-next'

import { api, safeErrorMessage } from '@/api/client'
import ModalDialog from '@/components/ModalDialog.vue'
import { useConsoleStore } from '@/stores/console'
import type { Channel, ChannelInput } from '@/types'

const store = useConsoleStore()
const editChannel = ref<Channel | null>(null)
const deleteChannel = ref<Channel | null>(null)
const actionError = ref('')
const saving = ref(false)
const deleting = ref(false)
const searchQuery = ref('')
const sourceFilter = ref<'all' | 'managed' | 'native'>('all')
const statusFilter = ref<'all' | 'enabled' | 'disabled'>('all')
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

function clearFilters(): void {
  searchQuery.value = ''
  sourceFilter.value = 'all'
  statusFilter.value = 'all'
}

function onTargetChange(event: Event): void {
  void store.loadChannels((event.target as HTMLSelectElement).value)
}

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
      <button class="primary-button" type="button" @click="store.navigate('settings')">前往设置</button>
    </div>

    <section v-else class="workspace-panel" aria-label="渠道列表">
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
            class="icon-button"
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

      <div
        v-if="store.channelState === 'loading'"
        class="state-panel"
        role="status"
        aria-label="正在读取目标渠道"
      >
        <span class="spinner" aria-hidden="true"></span>
        <p>正在实时读取完整渠道列表</p>
      </div>

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
          显示 {{ filteredChannels.length }} / {{ store.channels.length }} 个渠道
        </p>

        <div v-if="filteredChannels.length === 0" class="state-panel filter-empty">
          <h2>没有匹配的渠道</h2>
          <p>调整搜索词或筛选条件后重试。</p>
          <button class="secondary-button" type="button" @click="clearFilters">清除筛选</button>
        </div>

        <div v-else class="table-scroll">
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
              <tr v-for="channel in filteredChannels" :key="channel.id">
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
                  </button>
                  <button
                    class="icon-button icon-button-small danger-icon"
                    type="button"
                    :aria-label="`删除渠道 ${channel.name}`"
                    title="删除渠道"
                    @click="deleteChannel = channel; actionError = ''"
                  >
                    <Trash2 :size="16" aria-hidden="true" />
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>
    </section>

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
