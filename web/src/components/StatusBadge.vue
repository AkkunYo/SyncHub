<script setup lang="ts">
import { computed } from 'vue'

import type { MatrixStatus } from '@/types'

const props = defineProps<{
  status: MatrixStatus
}>()

const labels: Record<MatrixStatus, string> = {
  unsynced: '未同步',
  synced: '已同步',
  drifted: '有漂移',
  incompatible: '目标不兼容',
  needs_reconcile: '需要校验',
}

const label = computed(() => labels[props.status])
</script>

<template>
  <span class="status-badge" :class="`status-${status}`">{{ label }}</span>
</template>
