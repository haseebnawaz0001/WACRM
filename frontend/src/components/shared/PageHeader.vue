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
    <!--
      The header wraps rather than running off the edge.

      Every page in the app puts its actions in this bar, and on a phone they
      simply overflowed: Contacts showed "Contacts | Filters | Contact Fields |
      Impo…" with the rest of the toolbar past the right edge and no way to
      reach it. One row when there is room, two when there is not.
    -->
    <div class="flex min-h-16 flex-wrap items-center gap-y-2 px-6 py-3 max-md:px-4">
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
      <div class="min-w-0 flex-1 basis-40">
        <h1 class="truncate text-xl font-semibold text-white light:text-gray-900">{{ title }}</h1>
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
      <!-- The actions keep together and take their own line when the title
           needs the width. -->
      <div class="flex flex-wrap items-center gap-2 max-md:w-full">
        <slot name="actions" />
      </div>
    </div>
  </header>
</template>
