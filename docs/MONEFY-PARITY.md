# Monefy parity — the screen contract

This is the specification the mobile UI is measured against, read off reference screenshots of
Monefy Premium (591×1280). When a phase-3 component looks "close enough", check it against this
document rather than against memory.

**Scope note.** We reproduce *layout and interaction flow*. Monefy's drawn category icons, its
script wordmark and its name are not reproduced — see the legal boundary in `CLAUDE.md`.

> §1 and §2 are built: `web-ui/src/components/monefy/*`, assembled in `views/DashboardView.vue` and
> `views/RecordView.vue`. The keypad's arithmetic lives in `lib/calculator.ts` and the period model in
> `lib/period.ts`; both are tested there. Still open in §1: the account filter behind the left
> drawer's top row, search, and transfers.

---

## 1. Main screen

```
┌──────────────────────────────────────────────┐
│ ▽   moneyfly            🔍   ↔   ⋮           │  green header, 56px tall
│     All accounts                             │  subtitle = active account filter
├──────────────────────────────────────────────┤
│   July          August          September    │  month carousel, horizontal swipe
├──────────────────────────────────────────────┤
│  ≡      [ Balance   -€317.03 ]        ⇩🏷     │  view toggle · balance pill · sort
├──────────────────────────────────────────────┤
│                                              │
│        donut  OR  category list              │
│                                              │
├──────────────────────────────────────────────┤
│        ( − )                ( + )            │  record buttons, ~80px circles
└──────────────────────────────────────────────┘
```

### Header

| Control | Behaviour |
| --- | --- |
| `▽` funnel (left) | Opens the **left drawer** — account filter *and* period selector. |
| Title + subtitle | Wordmark over the active filter ("All accounts"). |
| `🔍` | Search with filters (category, account, date range, amount range). |
| `↔` | Transfer between accounts. |
| `⋮` | Opens the **right drawer**. |

### The two drawers

Neither is a dropdown menu. Both slide in over the dashboard, dimming it, and are dismissed by
tapping the dimmed area.

**Left drawer** (`▽`) — what is being shown:

```
┌──────────────────────┐
│ 💵 All accounts      │  ← account filter, with its currency underneath
│    EUR               │
├──────────────────────┤
│  Day                 │
│  Week                │
│  Month        ◀ active, filled green
│  Year                │
│  All                 │
│  Interval            │
├──────────────────────┤
│  Choose date         │
└──────────────────────┘
```

**This is the discovery that reshapes the dashboard: the period is not always a month.** The carousel
and every total are over the *selected period*, whatever its length. `Interval` and `Choose date` open
pickers. The month carousel becomes a period carousel showing the previous/current/next period.

**Right drawer** (`⋮`) — where to go: Categories, Accounts, Currencies, Settings, Guides. Large icon
above each label, one per row.

### Category icons around the donut

They sit on the **border cells of a grid that fills the whole area** — straight columns down the left
and right, straight rows across the top and bottom, with the donut in the hole.

The grid is derived from a **target cell size**, not from the number of categories. Sizing it to fit
every category is what produces nine thin rows and a shrunken chart; the reference keeps the frame
and the chart readable and simply does not draw what does not fit (its own screenshot shows fourteen
of nineteen categories). The ring's outer edge *is* the space allotted to it, and its hole is 60% of
that — a thinner ring reads as a much smaller chart even when the space is identical.

This is worth stating plainly because the obvious implementations are all wrong. Any *curve* — a
circle, a superellipse, a rounded rectangle — spaces icons by angle, so the gaps stretch and squeeze
as the curve turns and the result looks scattered no matter how the maths is tuned. Only a grid gives
equal spacing along each edge. The grid is sized to the category count (`4×4` up to `7×7`, capacity
`4n − 4`); categories beyond its capacity are not drawn here and stay one tap away in the record
screen's grid.

A category with a slice takes the free cell **nearest its slice's angle**, largest slice first, so its
leader line is short and runs outward. That is also why the leader can be a straight line: it never
has to cross the hole where the totals are.

**Every icon is a button.** Tapping one — used or unused — starts an expense already assigned to that
category, so a repeat purchase is: tap the icon, type the amount, save. Two taps.

### Period carousel

The previous and next period sit either side of the current one, dimmed. Changing period works two
ways — a horizontal swipe anywhere on the body, and a tap on a neighbouring label.

**The swipe is a drag, not a tap-equivalent.** The content follows the finger and animates the rest
of the way on release, or springs back if the gesture was too short. A discrete jump on release reads
as a bug even when the resulting state is right.

### View toggle row

`≡` on the left switches between donut and list. The balance pill is centred; it is red when the
period is negative. `⇩🏷` on the right controls sort order. In donut mode a second `≡` appears on the
right of the pill (the screenshot shows the pill flanked by two of them).

**The balance pill is also a handle.** Swiping it up — or tapping it — opens a panel listing every
record in the period, grouped by day, newest first. Dragging it back down, or tapping outside, closes
it.

That panel must not look like a different application. It was once a short bottom sheet with its own
row design and a text `Close` button, so the gesture landed you somewhere unrecognisable — the
complaint that prompted rewriting it. It is near-full height, on the dashboard's own background, with
the same transaction rows as list mode and the period title in the same green as the carousel. The
only control is a chevron.

**Any gesture here must check that a button is held.** A mouse emits `pointermove` while hovering, so
a handler reading coordinates alone treats crossing the window as a drag — and once it takes
`setPointerCapture`, it swallows every click from its children. That shipped once: the period paged
itself as the cursor moved, and the balance pill never received its own gesture.

### List mode

One row per category, sorted by amount descending:

`⌄ chevron · category icon in its colour · name · green count badge · amount in red, right-aligned`

The count badge sits **against the name**, not out at the right margin — out there it reads as part
of the amount rather than as a count of what is in the category.

Expanding a row reveals its individual transactions, drawn as:

```
●  ₴320.00                                        4 Aug
   €6.40
   Note, if there is one
```

- a **dot in the category's colour**, not the icon: the category is named in the row directly above,
  and repeating its icon on every line turns a list into a column of pictures;
- the amount **in the currency it was recorded in**, large;
- the **base-currency value small underneath, and only when the currencies differ** — printing
  "€6.40" under "€6.40" is noise on every row;
- a short date (`4 Aug`) at the right;
- **no `Edit` link and no `Delete` button.** The whole row is the control — a bigger target and one
  fewer thing to read — and it opens the record for editing. A delete beside every amount is one
  mis-tap from losing a record, so deleting lives on the edit screen with an undo.

The same row component is used in the records panel and in search. One design for a transaction,
everywhere.

### Donut mode

- An SVG ring of per-category slices.
- Percentage labels outside the ring with leader lines to their slice.
- Icons for **every** category arranged around the ring; unused ones are dimmed rather than hidden.
- The ring's centre holds two lines: income in green above expenses in red (`€0.00` over `-€317.03`).

Three things about this chart are less obvious than they look, and all three were got wrong once:

* **Slices are `stroke-dasharray` on a `<circle>`, not arc paths.** No large-arc flag to get wrong at
  50%, and a single category taking 100% of the month still renders.
* **Labels are nudged apart, leader lines are not.** A real month is one dominant category plus a
  tail of tiny ones whose slice midpoints are within a degree or two of each other, so the labels
  must be spread — but each leader line still starts at the slice's true angle. Spread them too far
  and the whole tail migrates to the other side of the chart.
* **The leader's middle segment is an arc, not a chord.** A straight line to a nudged label cuts
  through the doughnut hole, straight over the month's totals.

Dimmed icons are placed only at angles that are clear of a label, by scanning the circle — dividing
the gaps proportionally puts one squarely on top of a percentage.

### Record buttons

Two large circles on the bottom edge: red `−` (expense) on the left, green `+` (income) on the
right. White fill, thick coloured border, coloured glyph. They must sit above
`env(safe-area-inset-bottom)` so the iPhone home indicator never overlaps them.

---

## 2. Record screen

One screen, two steps. A back chevron on the left of the header, `New expense` / `New income`
centred, a "make recurring" control on the right.

**The same screen edits an existing record** (`/edit/:id`): the fields are identical, and sending
someone somewhere that looks different to fix the thing they got wrong three taps ago is
disorienting. Editing shows `Edit expense` and swaps the recurring control for a bin, which deletes
with an undo toast rather than a confirmation dialogue — a modal before every delete only trains
people to dismiss it. The save reuses the row id, so it travels as an ordinary last-write-wins op.

Opening an existing record seeds the keypad with its amount in *append* mode: the first digit
extends the figure rather than wiping it. Backspace is how you start over.

### Step A — amount

```
🗓 Monday, 3 August  ⌄          ← tap opens the date picker, defaults to today
┌────────────────────────────┐
│ 💵                 0    ⌫  │   ← currency/account chip left, amount right
│ EUR                        │
└────────────────────────────┘
✎ Add note                      ← visible on this step only
┌───┬───┬───┬───┐
│ 1 │ 2 │ 3 │ + │
│ 4 │ 5 │ 6 │ − │
│ 7 │ 8 │ 9 │ * │
│ . │ 0 │ = │ / │
└───┴───┴───┴───┘
[     CHOOSE CATEGORY      ]    ← full-width, advances to step B
```

The date row is a real button that calls `showPicker()`, with a visible chevron. Overlaying a
transparent `<input type="date">` on the row — the obvious implementation — works on a phone but is
dead on desktop Chrome and Firefox, where only the calendar indicator opens the picker and that
indicator is exactly what the transparency hides.

**The currency chip is the account picker**, and the account decides the currency: an expense is
denominated in whatever paid for it. Writing every record in the base currency and then choosing an
account to match puts a euro expense on a forint wallet the moment the two disagree.

The keypad is **the app's own**, not the OS keyboard: bigger targets, no layout shift, no zoom. The
operator keys are real — it is a calculator, so `12.40 + 3` then `=` is a valid way to enter a total.

Details that are easy to get wrong, all covered by `lib/calculator.test.ts`:

* **A pending operation is folded in without `=`.** Tapping a category saves immediately, so
  `28.40 / 3` followed by a category must record 9.47, not 3.
* **The decimal point respects the currency.** A third decimal in EUR is refused at the keypad rather
  than accepted and rounded away later — otherwise the amount saved is not the amount on screen. In
  HUF the point is inert.
* **Dividing by zero refuses instead of clearing.** A calculator that blanks itself loses the amount
  you already typed; here the divisor is simply correctable.
* **Backspace on a result clears it**, rather than editing a digit of a number nobody typed.

`CHOOSE CATEGORY` is disabled while the amount is zero — a deviation from the reference, and a
deliberate one: there is nothing to record.

### Step B — category

A 4-column grid of tiles, each an outline icon over its label in the category's colour. The last
cell is a green `+` that creates a category. **Tapping a category saves immediately** and returns to
the main screen. Going back from step B returns to step A rather than leaving the
screen — which is why the two steps are one route, not two.

There is deliberately **no confirmation toast**: it would cover the bottom of the dashboard you were
just returned to — the record buttons included — and the new record is on the chart the moment you
land, which is confirmation enough. Undo is to tap the record and delete it from the edit screen.

The dashboard jumps to the period the record belongs to rather than staying on whichever one happened
to be open; recording something dated last week and landing on a chart that does not contain it looks
exactly like the record was lost.

Three taps total: `−` → digits → `CHOOSE CATEGORY` → category. (Monefy advertises two taps; that
counts the case where the amount is already entered.)

---

## 3. Default categories

Expense: Appliance, Bills, Clothes, Communication, Eating out, Entertainment, Family, Food, Gifts,
Health, Hobby, Hotel/Trip, House, Services, Sports, Studying, Taxi, Toiletry, Transport.

Income: Salary, Deposits, Savings, Other.

---

## 4. Theme tokens

Defined once in `web-ui/src/assets/tailwind.css` under `@theme`. Current values are eyedropper
estimates and may be corrected — but only there, never in a component.

| Token | Value | Used for |
| --- | --- | --- |
| `mf-green` | `#7ec89f` | header, amount field, `+` button |
| `mf-green-dark` | `#4f9c76` | active month, text on light |
| `mf-bg` | `#eefaf1` | screen background |
| `mf-red` | `#f0857b` | balance pill when negative |
| `mf-red-text` | `#e2544a` | expense amounts |
| `mf-muted` | `#b9cfc2` | inactive month, dimmed icons |

Plus a 20-entry `cat-*` palette for categories. A category row stores the palette **key**, never a
hex value.

### Motion

Two tokens in the same block, and every panel in the app uses them, so nothing reads as belonging to
a different application:

| Token | Value |
| --- | --- |
| `--mf-ease` | `cubic-bezier(0.22, 0.61, 0.36, 1)` |
| `--mf-duration` | `200ms` |

The curve decelerates hard at the end, which is what makes a sheet look like it settled rather than
stopped. Five named transitions build on them — `mf-fade`, `mf-sheet`, `mf-drawer-l`, `mf-drawer-r`,
`mf-page` — and they are applied by wrapping the `v-if` **at the call site**, because a `v-if` on its
own cannot animate a departure and the departure is the half people notice. All of it collapses under
`prefers-reduced-motion: reduce`; none of it is load-bearing.

---

## 4a. Multi-currency

Every total on the dashboard is in the user's base currency. A record keeps the currency of the
account that paid for it and is converted at the rate **on the day it happened** — not today's, or
last March's total would move every time the app was opened.

A row whose rate is unknown is left out of the totals **and counted in a banner**. It is not
silently dropped: that was the pre-phase-5 behaviour and the effect was a month that looked cheaper
than it was. The transaction row shows the original amount either way.

The `Currencies` screen in the right drawer lists each cached rate with its age. Age is the point: a
provider that stops publishing raises no error anywhere, it just stops moving, and every total
quietly keeps using an old number.

---

## 5. Monefy CSV format

Verified against a real export (`monefy-2023-12-03_03-48-39.csv`, 1,683 rows, 19.07.2021 →
03.12.2023). This governs both the importer (phase 8) and the Monefy-compatible export profile
(phase 9).

```
date,account,category,amount,currency,converted amount,currency,description
19.07.2021,UAH,Utilities,-65,UAH,-65,UAH,Коммисия
03.12.2023,EUR,Eating out,-10,EUR,-397.3,UAH,
03.12.2023,HUF,Communication,-1000,HUF,-100,UAH,
```

| Property | Reality | Consequence |
| --- | --- | --- |
| Encoding | UTF-8 **with BOM** | Strip it or the first header becomes `﻿date`. |
| Header | `currency` appears **twice** | **Header-name maps are impossible — address columns positionally.** |
| Date | `DD.MM.YYYY`, dots | Not `M/D/YYYY`. No time component at all. |
| Transaction id | none | Dedup must be structural (natural key + occurrence index). |
| Type column | none | The sign carries it. |
| Income rows | none in this export | Monefy does not export income here. |
| Columns 4–5 | native amount + currency | **Authoritative.** |
| Columns 6–7 | amount converted to the export-time base | **Discarded** — the base is whatever the phone had that day. |
| `description` | may be empty, quoted, or have trailing spaces | Trim for display, keep raw for the natural key. |
| Locale | older exports are Russian (`Наличные`, `Счета`) | Categories and accounts get renamed across exports; resolve through an alias table. |

### The failure this design exists to prevent

A naive importer that looks up hardcoded category names lost **≈237 of 1,683 rows (14%)** silently:

| CSV had | Importer expected | Rows lost |
| --- | --- | --- |
| `Utilities` | *nothing* | 119 |
| `HotelTrip` | `Hotel/Trip` | 58 |
| `Communication` | `Communications` | 25 |
| `Clouth` | `Clothes` | 19 |
| `Studing` | `Studying ` | 12 |
| `Sport` | `Sports` | 3 |
| `Taxi` | *nothing* | 1 |

**Therefore an unrecognised category blocks the batch.** It is never auto-created, never coerced,
never zeroed — the import screen makes the operator map it. Silent auto-creation is how `Utilities`
vanished; silent lookup failure is how `Communication` did.
