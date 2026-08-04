import {
  Baby,
  Bike,
  Building2,
  CarTaxiFront,
  CircleEllipsis,
  Cpu,
  Gift,
  House,
  Landmark,
  Laptop,
  Martini,
  PersonStanding,
  Phone,
  PiggyBank,
  Shirt,
  ShoppingBasket,
  ShowerHead,
  Sofa,
  Tag,
  Thermometer,
  TrainFront,
  Utensils,
  Wallet,
} from '@lucide/vue'
import type { Component } from 'vue'

/**
 * The icons a category may use.
 *
 * Lucide, not Monefy's drawn artwork — we reproduce the layout and the flow, not
 * the assets (see the legal boundary in CLAUDE.md). Categories store the *name*,
 * so this map is also the allow-list: an unknown name falls back rather than
 * rendering nothing.
 */
export const CATEGORY_ICONS: Record<string, Component> = {
  Baby,
  Bike,
  Building2,
  CarTaxiFront,
  CircleEllipsis,
  Cpu,
  Gift,
  House,
  Landmark,
  Laptop,
  Martini,
  PersonStanding,
  Phone,
  PiggyBank,
  Shirt,
  ShoppingBasket,
  ShowerHead,
  Sofa,
  Tag,
  Thermometer,
  TrainFront,
  Utensils,
  Wallet,
}

export const FALLBACK_ICON = 'CircleEllipsis'

export function iconFor(name: unknown): Component {
  return CATEGORY_ICONS[String(name)] ?? CATEGORY_ICONS[FALLBACK_ICON]
}

/**
 * The category colour palette.
 *
 * A category stores the **key**, never a hex value. Re-theming then costs one
 * edit in tailwind.css instead of rewriting every synced row on every device.
 */
export const CATEGORY_COLORS = [
  'violet',
  'purple',
  'gold',
  'amber',
  'orange',
  'rose',
  'pink',
  'magenta',
  'red',
  'coral',
  'green',
  'lime',
  'sage',
  'blue',
  'indigo',
  'steel',
  'stone',
  'gray',
  'slate',
  'teal',
] as const

export type CategoryColor = (typeof CATEGORY_COLORS)[number]

/** The CSS custom property behind a palette key, for SVG fills and inline styles. */
export function colorVar(color: unknown): string {
  const key = String(color)
  return (CATEGORY_COLORS as readonly string[]).includes(key)
    ? `var(--color-cat-${key})`
    : 'var(--color-mf-muted)'
}

export interface Seed {
  /** Becomes the row id, e.g. `cat:food`. */
  key: string
  name: string
  kind: 'expense' | 'income'
  icon: string
  color: CategoryColor
}

/**
 * The categories a new account starts with — the set on the reference
 * screenshots, in the same alphabetical order.
 *
 * The id is derived from `key`, so it is **the same on every device**. That is
 * what makes seeding safe without any coordination: if two fresh devices sign in
 * at once and both seed, they produce identical ids and last-write-wins merges
 * them into one set instead of leaving nineteen duplicates.
 */
export const DEFAULT_CATEGORIES: Seed[] = [
  { key: 'appliance', name: 'Appliance', kind: 'expense', icon: 'Sofa', color: 'violet' },
  { key: 'bills', name: 'Bills', kind: 'expense', icon: 'Tag', color: 'gold' },
  { key: 'clothes', name: 'Clothes', kind: 'expense', icon: 'Shirt', color: 'purple' },
  { key: 'communication', name: 'Communication', kind: 'expense', icon: 'Phone', color: 'gray' },
  { key: 'eating-out', name: 'Eating out', kind: 'expense', icon: 'Utensils', color: 'sage' },
  { key: 'entertainment', name: 'Entertainment', kind: 'expense', icon: 'Martini', color: 'orange' },
  { key: 'family', name: 'Family', kind: 'expense', icon: 'Baby', color: 'pink' },
  { key: 'food', name: 'Food', kind: 'expense', icon: 'ShoppingBasket', color: 'rose' },
  { key: 'gifts', name: 'Gifts', kind: 'expense', icon: 'Gift', color: 'stone' },
  { key: 'health', name: 'Health', kind: 'expense', icon: 'Thermometer', color: 'red' },
  { key: 'hobby', name: 'Hobby', kind: 'expense', icon: 'Cpu', color: 'lime' },
  { key: 'hotel-trip', name: 'Hotel/Trip', kind: 'expense', icon: 'Building2', color: 'indigo' },
  { key: 'house', name: 'House', kind: 'expense', icon: 'House', color: 'blue' },
  { key: 'services', name: 'Services', kind: 'expense', icon: 'Bike', color: 'magenta' },
  { key: 'sports', name: 'Sports', kind: 'expense', icon: 'PersonStanding', color: 'slate' },
  { key: 'studying', name: 'Studying', kind: 'expense', icon: 'Laptop', color: 'green' },
  { key: 'taxi', name: 'Taxi', kind: 'expense', icon: 'CarTaxiFront', color: 'amber' },
  { key: 'toiletry', name: 'Toiletry', kind: 'expense', icon: 'ShowerHead', color: 'steel' },
  { key: 'transport', name: 'Transport', kind: 'expense', icon: 'TrainFront', color: 'coral' },

  { key: 'salary', name: 'Salary', kind: 'income', icon: 'Wallet', color: 'green' },
  { key: 'deposits', name: 'Deposits', kind: 'income', icon: 'Landmark', color: 'blue' },
  { key: 'savings', name: 'Savings', kind: 'income', icon: 'PiggyBank', color: 'teal' },
  { key: 'other', name: 'Other', kind: 'income', icon: 'CircleEllipsis', color: 'gray' },
]

export const categoryId = (key: string) => `cat:${key}`
export const DEFAULT_ACCOUNT_ID = 'acc:cash'
