<script setup lang="ts">
import { Button } from '@/components/ui/button'
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from '@/components/ui/breadcrumb'
import { ArrowLeft } from 'lucide-vue-next'
import type { Component } from 'vue'

defineProps<{
  title: string
  description?: string
  icon?: Component
  backLink?: string
  breadcrumbs?: Array<{ label: string; href?: string }>
}>()
</script>

<template>
  <header class="border-b border-white/[0.08] light:border-gray-200 bg-[#0a0a0b]/95 light:bg-white/95 backdrop-blur">
    <div class="flex h-16 items-center px-6">
      <RouterLink v-if="backLink" :to="backLink">
        <Button variant="ghost" size="icon" class="mr-3">
          <ArrowLeft class="h-5 w-5" />
        </Button>
      </RouterLink>
      <!--
        The page's glyph, flat.

        Every view used to hand this header a gradient of its own — thirty-eight
        of them, from "blue-500 to indigo-600" to "yellow-500 to orange-600" —
        so each page opened with a saturated plaque in a different hue, none of
        which meant anything. The sidebar already says which page you are on;
        this is a quiet marker beside the title, not a badge announcing it.
      -->
      <component
        v-if="icon"
        :is="icon"
        class="mr-2.5 h-5 w-5 shrink-0 text-white/40 light:text-gray-400"
        aria-hidden="true"
      />
      <div class="flex-1">
        <h1 class="text-xl font-semibold text-white light:text-gray-900">{{ title }}</h1>
        <template v-if="breadcrumbs?.length">
          <Breadcrumb>
            <BreadcrumbList>
              <template v-for="(crumb, index) in breadcrumbs" :key="index">
                <BreadcrumbItem>
                  <BreadcrumbLink v-if="crumb.href" :href="crumb.href">
                    {{ crumb.label }}
                  </BreadcrumbLink>
                  <BreadcrumbPage v-else>{{ crumb.label }}</BreadcrumbPage>
                </BreadcrumbItem>
                <BreadcrumbSeparator v-if="index < breadcrumbs.length - 1" />
              </template>
            </BreadcrumbList>
          </Breadcrumb>
        </template>
        <p v-else-if="description" class="text-sm text-white/50 light:text-gray-500">
          {{ description }}
        </p>
      </div>
      <slot name="actions" />
    </div>
  </header>
</template>
