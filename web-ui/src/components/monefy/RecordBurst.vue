<script setup lang="ts">
import { computed, onMounted, onScopeDispose } from 'vue'

import { colorVar } from '@/lib/categories'
import type { Burst } from '@/stores/recordBurst'
import CategoryIcon from './CategoryIcon.vue'
import MoneyAmount from './MoneyAmount.vue'

/**
 * What rises out of a record button after a new record is saved.
 *
 * The way reactions float up a live stream: the amount you just entered lifts
 * off the button in a chip, and a little stream of the category's icon rises
 * around it, each one swaying side to side on its own rhythm before it fades.
 * It answers "did that save, and as what?" without a toast — the notice that
 * used to sit over the very buttons it was confirming (see RecordView's
 * comment on staying silent) — and it is gone in about two seconds.
 *
 * It lives *inside* the button's own box (RecordFabs), so it starts from the
 * button wherever the layout puts it, with nothing to measure. It never takes a
 * tap: `pointer-events-none` throughout, so a finger going for the next record
 * lands on the button underneath, not on something passing over it.
 *
 * The motion itself is three CSS keyframes in tailwind.css (`mf-burst-*`); this
 * component only rolls the dice for each particle.
 */
const props = defineProps<{ burst: Burst }>()
const emit = defineEmits<{ done: [] }>()

/** Let the dashboard's own entrance mostly finish before anything rises over it. */
const START_MS = 180
const PARTICLES = 8
/** Stagger between particles, so they read as a stream and not a volley. */
const STAGGER_MS = 85

const rand = (lo: number, hi: number) => lo + Math.random() * (hi - lo)
const either = () => (Math.random() < 0.5 ? -1 : 1)

/**
 * Drift towards the middle of the screen: the expense button is on the left,
 * income on the right, and a stream leaning off the edge looks lost.
 */
const inward = props.burst.kind === 'expense' ? 1 : -1
/** Rises are a share of the screen, so a tall phone gets a taller stream. */
const height = typeof window === 'undefined' ? 800 : window.innerHeight

/** The category colour mixed into the surface — tinted, not a hex in sight. */
const tint = computed(() => colorVar(props.burst.color))

interface Particle {
  size: number
  vars: Record<string, string>
  end: number
}

function particle(i: number): Particle {
  const dur = Math.round(rand(1150, 1650))
  const delay = START_MS + 140 + i * STAGGER_MS + Math.round(rand(0, 50))
  return {
    size: Math.round(rand(26, 40)),
    end: delay + dur,
    vars: {
      '--rise': `${Math.round(height * rand(0.3, 0.55))}px`,
      '--drift': `${Math.round(rand(-24, 52) * inward)}px`,
      '--sway': `${Math.round(rand(12, 28)) * either()}px`,
      '--tilt': `${Math.round(rand(6, 16))}deg`,
      '--dur': `${dur}ms`,
      '--sway-dur': `${Math.round(rand(800, 1200))}ms`,
      '--delay': `${delay}ms`,
    },
  }
}

const particles = Array.from({ length: PARTICLES }, (_, i) => particle(i))

/** The amount chip: steadier and slower than the stream, so it can be read. */
const CHIP_MS = 1750
const chipVars = {
  '--rise': `${Math.round(height * 0.3)}px`,
  '--drift': `${28 * inward}px`,
  '--sway': `${5 * either()}px`,
  '--tilt': '2deg',
  '--dur': `${CHIP_MS}ms`,
  '--sway-dur': '1400ms',
  '--delay': `${START_MS}ms`,
}

const ringVars = { '--delay': `${START_MS}ms` }

/*
 * Leave once the last particle has faded. A timer rather than `animationend`:
 * under reduced motion every animation collapses to nothing and the events
 * arrive all at once, but a timer means one code path either way.
 */
const lifetime = Math.max(START_MS + CHIP_MS, ...particles.map((p) => p.end)) + 100
let timer: ReturnType<typeof setTimeout> | undefined
onMounted(() => {
  timer = setTimeout(() => emit('done'), lifetime)
})
onScopeDispose(() => clearTimeout(timer))
</script>

<template>
  <div class="pointer-events-none absolute inset-0 z-20" aria-hidden="true">
    <!-- The button acknowledges first: one ring, out and gone. -->
    <span
      class="mf-burst-ring absolute inset-0 rounded-full border-[3px]"
      :class="burst.kind === 'expense' ? 'border-mf-red' : 'border-mf-green'"
      :style="ringVars"
    />

    <span v-for="(p, i) in particles" :key="i" class="absolute top-1/2 left-1/2" :style="p.vars">
      <span class="mf-burst-rise block">
        <span class="mf-burst-sway block">
          <span
            class="mf-burst-pop grid -translate-x-1/2 -translate-y-1/2 place-items-center rounded-full shadow-sm"
            :style="{
              width: `${p.size}px`,
              height: `${p.size}px`,
              backgroundColor: `color-mix(in srgb, ${tint} 16%, var(--color-mf-surface))`,
              boxShadow: `0 0 0 1.5px color-mix(in srgb, ${tint} 40%, transparent)`,
            }"
          >
            <CategoryIcon :icon="burst.icon" :color="burst.color" :size="Math.round(p.size * 0.6)" />
          </span>
        </span>
      </span>
    </span>

    <!-- Last in the DOM so the stream passes behind it. -->
    <span class="absolute top-1/2 left-1/2" :style="chipVars">
      <span class="mf-burst-rise block">
        <span class="mf-burst-sway block">
          <span
            class="mf-burst-pop flex -translate-x-1/2 -translate-y-1/2 items-center gap-1.5 rounded-full bg-mf-surface py-1.5 pr-3.5 pl-2.5 whitespace-nowrap shadow-lg"
          >
            <CategoryIcon :icon="burst.icon" :color="burst.color" :size="22" />
            <span
              class="flex text-base font-semibold"
              :class="burst.kind === 'expense' ? 'text-mf-red-text' : 'text-mf-green-dark'"
            >
              <!-- Income has no sign of its own; an expense brings its minus. -->
              <span v-if="burst.kind === 'income'">+</span>
              <MoneyAmount :minor="burst.amountMinor" :currency="burst.currency" />
            </span>
          </span>
        </span>
      </span>
    </span>
  </div>
</template>
