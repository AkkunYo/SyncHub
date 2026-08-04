<script setup lang="ts">
withDefaults(defineProps<{ label: string; columns?: number; rows?: number }>(), {
  columns: 6,
  rows: 5,
})
</script>

<template>
  <div class="table-loading-state" role="status" :aria-label="label">
    <div class="table-skeleton" aria-hidden="true">
      <div v-for="row in rows" :key="row" class="skeleton-row">
        <span class="skeleton-cell skeleton-cell-primary"></span>
        <span v-for="column in Math.max(columns - 1, 1)" :key="column" class="skeleton-cell"></span>
      </div>
    </div>
  </div>
</template>
