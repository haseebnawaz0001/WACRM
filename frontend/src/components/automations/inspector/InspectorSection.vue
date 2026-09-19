<script setup lang="ts">
/** One titled block of the inspector. Sections separate by space and a rule, not boxes. */
defineProps<{ title?: string; hint?: string }>()
</script>

<template>
  <section class="border-b border-white/[0.06] px-5 py-5 last:border-b-0 light:border-gray-100">
    <h3 v-if="title" class="text-[13px] font-semibold text-white light:text-gray-900">{{ title }}</h3>
    <p v-if="hint" class="mt-1 text-xs leading-relaxed text-muted-foreground">{{ hint }}</p>
    <div :class="title || hint ? 'mt-3' : ''">
      <slot />
    </div>
  </section>
</template>
