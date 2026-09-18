<script setup lang="ts">
/**
 * A state, as a dot and a word.
 *
 * Filled badges are a highlight, and a column where nine rows in ten say the
 * same thing has nothing to highlight — the templates list was a wall of green
 * "Approved" with the one rejected template the quietest mark on the page. A
 * dot keeps the colour doing its job at a size that does not compete with the
 * row's own content, and the word stays readable for anyone who cannot tell
 * the dots apart.
 */
const props = withDefaults(defineProps<{
  label: string
  /** What the state means, not what colour to paint. */
  tone?: 'good' | 'warn' | 'bad' | 'neutral'
}>(), { tone: 'neutral' })

const DOT: Record<string, string> = {
  good: 'bg-emerald-400 light:bg-emerald-600',
  warn: 'bg-amber-400 light:bg-amber-600',
  bad: 'bg-red-400 light:bg-red-600',
  neutral: 'bg-white/30 light:bg-gray-400',
}
</script>

<template>
  <span class="inline-flex items-center gap-2 whitespace-nowrap text-sm">
    <span :class="['h-1.5 w-1.5 shrink-0 rounded-full', DOT[props.tone]]" aria-hidden="true" />
    <span>{{ props.label }}</span>
  </span>
</template>
