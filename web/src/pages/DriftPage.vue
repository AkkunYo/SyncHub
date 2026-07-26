<script setup lang="ts">
import { ref } from 'vue'
import { Check, RefreshCw } from 'lucide-vue-next'

import { api, safeErrorMessage } from '@/api/client'
import { useConsoleStore } from '@/stores/console'
import type { DriftItem } from '@/types'

const store = useConsoleStore()
const acceptingKey = ref('')
const reconciling = ref(false)
const notice = ref('')
const error = ref('')

const fieldLabels: Record<string, string> = {
  models: '模型',
  group: '分组',
  priority: '优先级',
  weight: '权重',
}

function displayValue(value: unknown): string {
  if (Array.isArray(value)) return value.join(', ')
  if (value === null || value === undefined || value === '') return '空'
  return String(value)
}

async function accept(item: DriftItem): Promise<void> {
  const key = `${item.assetId}:${item.targetId}`
  acceptingKey.value = key
  notice.value = ''
  error.value = ''
  try {
    await api.acceptDrift(item.targetId, {
      upstream_asset_id: item.assetId,
      channel_id: item.channelId,
    })
    store.markDriftAccepted(item.assetId, item.targetId)
    notice.value = '漂移已接受'
  } catch (reason) {
    error.value = safeErrorMessage(reason)
  } finally {
    acceptingKey.value = ''
  }
}

async function reconcileAll(): Promise<void> {
  if (store.targets.length === 0) return
  reconciling.value = true
  notice.value = ''
  error.value = ''
  const outcomes = await Promise.allSettled(store.targets.map((target) => api.reconcile(target.id)))
  if (outcomes.some((outcome) => outcome.status === 'rejected')) {
    error.value = '部分目标校验失败，请重试'
  } else {
    notice.value = '目标校验完成'
  }
  try {
    await store.loadMatrix()
  } catch {
    error.value = store.matrixError
  } finally {
    reconciling.value = false
  }
}
</script>

<template>
  <section class="page" aria-labelledby="drift-heading">
    <header class="page-header">
      <div>
        <p class="eyebrow">目标事实状态</p>
        <h1 id="drift-heading">配置漂移</h1>
      </div>
      <button
        class="secondary-button"
        type="button"
        :disabled="reconciling || store.targets.length === 0"
        @click="reconcileAll"
      >
        <RefreshCw :size="16" aria-hidden="true" />
        {{ reconciling ? '校验中' : '校验全部目标' }}
      </button>
    </header>

    <p v-if="notice" class="notice notice-success" role="status">{{ notice }}</p>
    <p v-if="error" class="notice notice-error" role="alert">{{ error }}</p>

    <div v-if="store.driftItems.length === 0" class="state-panel">
      <Check :size="24" aria-hidden="true" />
      <h2>当前没有配置漂移</h2>
    </div>

    <div v-else class="drift-list">
      <article v-for="item in store.driftItems" :key="`${item.assetId}:${item.targetId}`" class="drift-row">
        <header>
          <div>
            <strong>{{ item.assetName }}</strong>
            <small>{{ item.targetName }} / #{{ item.channelId }}</small>
          </div>
          <button
            class="secondary-button"
            type="button"
            :disabled="acceptingKey === `${item.assetId}:${item.targetId}`"
            :aria-label="`接受 ${item.targetName} 的目标端状态`"
            @click="accept(item)"
          >
            <Check :size="16" aria-hidden="true" />
            {{ acceptingKey === `${item.assetId}:${item.targetId}` ? '接受中' : '接受目标状态' }}
          </button>
        </header>
        <dl class="difference-list">
          <div v-for="difference in item.differences" :key="difference.field">
            <dt>{{ fieldLabels[difference.field] ?? difference.field }}</dt>
            <dd>{{ displayValue(difference.expected) }} -> {{ displayValue(difference.actual) }}</dd>
          </div>
        </dl>
      </article>
    </div>
  </section>
</template>
