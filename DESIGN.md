---
name: WA CRM
description: A self-hosted WhatsApp CRM whose screens say what things do in plain words, on a near-black workbench with one emerald accent.
colors:
  background: "hsl(0 0% 2.5%)"
  card: "hsl(0 0% 5%)"
  popover: "hsl(0 0% 7%)"
  muted: "hsl(0 0% 10%)"
  border: "hsl(0 0% 12%)"
  foreground: "hsl(0 0% 98%)"
  muted-foreground: "hsl(0 0% 55%)"
  light-background: "hsl(0 0% 98%)"
  light-card: "hsl(0 0% 100%)"
  light-muted: "hsl(0 0% 96%)"
  light-border: "hsl(0 0% 90%)"
  light-foreground: "hsl(0 0% 9%)"
  light-muted-foreground: "hsl(0 0% 40%)"
  primary: "hsl(160 84% 39%)"
  primary-foreground: "hsl(0 0% 9%)"
  primary-link-light: "hsl(160 84% 27%)"
  destructive: "hsl(0 72% 51%)"
  question-violet: "#8b5cf6"
  wait-sky: "#0ea5e9"
  unfinished-amber: "#f59e0b"
  flow-edge: "rgba(255, 255, 255, 0.18)"
  flow-edge-light: "#cbd5e1"
  flow-dots: "rgba(255, 255, 255, 0.2)"
  flow-dots-light: "rgba(15, 23, 42, 0.26)"
typography:
  headline:
    fontFamily: "Inter, ui-sans-serif, system-ui, sans-serif"
    fontSize: "1.125rem"
    fontWeight: 600
    lineHeight: "1.75rem"
  title:
    fontFamily: "Inter, ui-sans-serif, system-ui, sans-serif"
    fontSize: "13.5px"
    fontWeight: 500
    lineHeight: "20px"
  section:
    fontFamily: "Inter, ui-sans-serif, system-ui, sans-serif"
    fontSize: "13px"
    fontWeight: 600
    lineHeight: 1.5
  body:
    fontFamily: "Inter, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.875rem"
    fontWeight: 400
    lineHeight: "1.25rem"
  prose:
    fontFamily: "Inter, ui-sans-serif, system-ui, sans-serif"
    fontSize: "13.5px"
    fontWeight: 400
    lineHeight: "1.5rem"
  detail:
    fontFamily: "Inter, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.75rem"
    fontWeight: 400
    lineHeight: "1rem"
  label:
    fontFamily: "Inter, ui-sans-serif, system-ui, sans-serif"
    fontSize: "11px"
    fontWeight: 500
    lineHeight: 1.5
rounded:
  sm: "2px"
  md: "4px"
  lg: "6px"
  full: "9999px"
spacing:
  inspector-section: "1.25rem"
  card-x: "0.875rem"
  card-y: "0.75rem"
  flow-gap-y: "64px"
  flow-gap-x: "56px"
  flow-branch-gap-y: "112px"
components:
  button-primary:
    backgroundColor: "{colors.primary}"
    textColor: "{colors.primary-foreground}"
    rounded: "{rounded.sm}"
    height: "2.25rem"
    padding: "0.5rem 1rem"
  button-primary-sm:
    backgroundColor: "{colors.primary}"
    textColor: "{colors.primary-foreground}"
    rounded: "{rounded.sm}"
    height: "2rem"
    padding: "0 0.75rem"
  field:
    backgroundColor: "rgba(255, 255, 255, 0.04)"
    textColor: "{colors.foreground}"
    rounded: "{rounded.sm}"
    padding: "0.5rem 0.75rem"
    height: "2.5rem"
  variable-chip:
    backgroundColor: "rgba(255, 255, 255, 0.1)"
    textColor: "#ffffff"
    rounded: "{rounded.md}"
    padding: "0 6px"
  flow-card:
    backgroundColor: "{colors.card}"
    textColor: "{colors.foreground}"
    rounded: "{rounded.lg}"
    padding: "0.75rem 0.875rem"
    width: "296px"
  flow-wait:
    backgroundColor: "{colors.card}"
    rounded: "{rounded.full}"
    height: "52px"
    width: "296px"
  add-step-button:
    backgroundColor: "#171717"
    textColor: "rgba(255, 255, 255, 0.7)"
    rounded: "{rounded.full}"
    size: "1.5rem"
  status-pill-on:
    backgroundColor: "rgba(16, 185, 129, 0.08)"
    textColor: "#a7f3d0"
    rounded: "{rounded.full}"
    padding: "0.25rem 0.625rem"
  status-pill-draft:
    backgroundColor: "rgba(245, 158, 11, 0.08)"
    textColor: "#fde68a"
    rounded: "{rounded.full}"
    padding: "0.25rem 0.375rem 0.25rem 0.625rem"
---

# Design System: WA CRM

## Overview

**Creative North Star: "The Plain-Spoken Workbench"**

WA CRM is a working tool for people who run a front desk, not a showcase. It is dark-first (a near-black page, a barely lifted card surface, hairline borders) with a clean white light mode bound to the `.light` class, set entirely in Inter on a 14px root, so every rem-based utility renders at seven eighths of Tailwind's nominal size (text-sm is 12.25px, text-xs 10.5px, h-9 31.5px). Density is moderate: rows and cards are compact, but every block breathes on space and a single rule rather than nested boxes.

Colour is spent as meaning, never as decoration. Emerald is the one accent and marks what is live, chosen, or the next action; every other hue on a screen answers one specific question. Words do the rest of the work: a card is a sentence, a status says what to do next, and a failure is told in the builder's own language. The automation builder is the fullest expression of this system: a path drawn top to bottom, whose cards read as plain sentences and whose rule reads back as prose.

**Key Characteristics:**
- Near-black and white themes from one set of HSL variables; emerald primary with a near-black label.
- One meaning per hue; amber is reserved for "something still needs finishing".
- Sentences over forms: chosen values in weight, connecting words muted.
- Flat surfaces separated by 1px borders and space; soft shadow only where something floats over the canvas.
- Small corners from a three-step scale (2px / 4px / 6px) plus full pills.

## Colors

A neutral greyscale in both themes with one emerald accent, and three path hues that each carry exactly one meaning.

### Primary
- **Brand Emerald** (`primary`): primary buttons (with a near-black label, 7.7:1), the focus ring, the selected card's border and ring, the active tab underline, a shown contact's lit path, the start/trigger tile, and an "On" status. In light mode, text links take the darker cut (`primary-link-light`, 5.0:1) of the same hue rather than a different colour.

### Secondary
- **Question Violet** (`question-violet`): only an if/else question: its icon tile (violet-500 at 15% with violet-300 glyph; violet-50 / violet-700 in light) and its diamond in the recipe gallery's path preview.
- **Wait Sky** (`wait-sky`): only a wait: its pill tile, the "N waiting here" count, and a "waiting" trace mark.

### Tertiary
- **Unfinished Amber** (`unfinished-amber`): only "something still needs finishing": problem lines under a card title (with a warning triangle), the border of an unfinished card (amber-500 at 45%; amber-400 in light), the draft status pill, the "Before it can run" list icons, and the unsaved-live-edit banner.
- **Destructive Red** (`destructive`): failures and removal only: a failed trace mark, a failing status pill, the failure count, delete actions.

### Neutral
- **Near-Black Page** (`background`) and **Card Surface** (`card`): the page and canvas sit on the page colour; cards, the inspector and the top bar sit one step up.
- **Popover** (`popover`): menus, the add-step menu, the hover toolbar on a card.
- **Hairline** (`border`, and white at 6-10% alpha in components): every divider and card edge.
- **Foreground / Muted Foreground**: text at 98% and 55% (light: 9% and 40%, lowered from 45% to clear 4.5:1 on tab strips).
- **Flow Edge / Flow Dots**: the path lines (1.5px) and the canvas dot grid, both faint by design.
- Actions on the path are neutral: their tile is white at 6% with a 70% glyph (gray-100 / gray-600 in light).

### Named Rules
**The One Meaning Per Hue Rule.** On the path, emerald starts it, is selected, or is lit; violet asks; sky waits; amber needs finishing; actions stay neutral. A hue never borrows another's meaning: the idle trigger card border is neutral, not emerald, and amber is never a step's own colour.

**The Quiet Emerald Rule.** Emerald is the action and the live state, not the brand wallpaper. A running rule's pill is emerald at 8% fill with a pale label; a link inside prose is the only text allowed to be green, and links elsewhere inherit their colour.

## Typography

**Body Font:** Inter (self-hosted, 400/500/600, with the system sans stack as fallback)

**Character:** One family, weight-driven. Hierarchy comes from 500 and 600 against 400, and from foreground against muted foreground, never from a second face or letter-spacing.

### Hierarchy
- **Headline** (600, 1.125rem = 15.75px): the editable rule name in the builder's top bar; empty-state headings.
- **Title** (500, 13.5px, 1.25rem line): a card's title, the sentence of what the step does.
- **Section** (600, 13px): inspector section headings ("What this does", "Before it can run").
- **Body** (400, 0.875rem = 12.25px): rows, fields, menu items, tabs. Page body copy is 1rem (14px) at 1.5.
- **Prose** (400, 13.5px, 1.5rem line): the rule read back as paragraphs in "What this does".
- **Detail** (400, 0.75rem = 10.5px, 1rem line): a card's detail line, hints under section titles, problem lines, the status pill.
- **Label** (500-600, 11px fixed): Yes/No branch labels, trace marks, waiting counts; tabular figures for counts.

### Named Rules
**The Weighted Value Rule.** In any sentence built from parts, the values a person chose are medium weight in foreground colour (white at 90% / gray-900) and the connecting words stay muted (white at 50% / gray-500). The sentence scans as its values.

**The No Eyebrow Rule.** A title starts the sentence itself ("When a tag is added"); nothing sits above it as a small uppercase label.

## Layout

The app shell is a left sidebar and a content column. List pages centre at up to 1024px (the automations list) or 1152px (the recipe gallery, a list plus a 360px side column at `lg`), with 16-24px gutters.

The builder is a full-height split: a top bar (min 56px) with back, editable name, status pill, "Try it on a contact", the primary action and a menu; a Build | History tab strip underlined in emerald; then the canvas filling the rest and a 400px inspector on the right from `lg` (1024px) up. Below `lg` the inspector opens as a bottom sheet capped at 85vh.

The canvas is auto-laid-out top to bottom; nobody drags. Cards are 296px wide, stacked 64px apart; a question's branches split 56px apart and start 112px below it, rejoining at a small ring. Card heights are measured from their words with the page font, every size taken from the root rem (14px) rather than assumed: 53px with no detail line, 57 with one, 71 with two (the start card adds its "who it is for" band). A + sits on every edge. Opening one clears the selection, and its menu fits the viewport (max 34rem or the available height) with a bottom fade when it scrolls.

Viewport rules: a phone (under 640px) opens at zoom 1 with the start card centred and pans; a desktop fits the rule but never shrinks below 0.6. Revealing a card pans the minimum amount at the current zoom (300ms). A shown trace fits the whole rule when it fits at a readable size (0.6 desktop, 0.75 phone), otherwise only the lit path.

Inspector sections are 1.25rem padded blocks separated by a single hairline, not boxes.

## Elevation & Depth

Flat by default, layered by tone: page, card, popover step up in lightness (2.5%, 5%, 7%) and are separated by hairlines. Shadows appear only on things that float over the canvas or the page: flow cards, the + buttons, trace marks, the card's hover toolbar and popovers.

### Shadow Vocabulary
- **Flow card rest** (`box-shadow: 0 1px 2px rgba(0,0,0,0.2), 0 4px 12px -6px rgba(0,0,0,0.35)`; light: 0.05 / 0.12): lifts a step card off the dot grid.
- **Start card rest** (`box-shadow: 0 1px 2px rgba(0,0,0,0.2), 0 6px 16px -8px rgba(0,0,0,0.4)`; light: 0.05 / 0.14): a touch deeper, because it anchors the path.
- **Selection ring** (2px emerald at 25% outside an emerald-at-70% border): selection is a ring, never a heavier shadow.

### Named Rules
**The Hairline Before Shadow Rule.** Separate with a 1px border and space first; a shadow is only for something that sits over the canvas.

## Shapes

Corners come only from the radius variables: 2px for fields, buttons and segmented controls; 4px for icon tiles, variable chips and small menus; 6px for cards, lists and panels; full rounds for pills, the +, trace marks, the End marker, and the join ring. Shape carries meaning on the path: an action or question is a 6px card, a wait is a slim full pill (52px), the End is a dashed pill, the rejoin is a 12px ring. Edges are smooth-step lines with a 12px corner radius.

## Components

### Buttons
Flat, compact, one colour straight off the token.
- **Shape:** gently squared (2px).
- **Primary:** emerald with a near-black label, 2.25rem default and 2rem small; hover drops to 90% opacity. It says the next thing to do ("Turn On", "Save Changes").
- **Destructive outline:** a red hairline (red-500 at 30%) with red text, red at 10% on hover. Used for Delete in a page bar, where only the primary is filled.
- **Labels:** buttons and actions are Title Case ("Add Contact", "New Follow-up", "Try It on a Contact"), with short words like "on", "a" and "from" kept lowercase mid-label.
- **Outline:** a hairline (white at 10%) over white at 2%; hover to 6% fill and 20% border. The Test button turns emerald-outlined while its panel is open.
- **Ghost:** muted text, white-at-6% hover; used for back, undo/redo and overflow.
- **Hover / Focus:** a 2px emerald focus ring with offset; press scales to 0.97.

### Chips
- **Variable chip:** a neutral chip (white at 10% with a 12% inset hairline; light: #eef2f6 with #dbe2ea), 4px corners, 6px side padding, no margin, 0.923em medium text, wearing the variable's plain name. In read-only text the same variable reads as ‹Label›. The `{{path}}` syntax is never shown.
- **Branch labels:** Yes is an emerald-tinted pill; No is a neutral pill. Both 11px semibold.
- **Trace marks:** 11px pills pinned to a card's top right: emerald for done, sky for waiting, neutral for skipped, red for failed, each with its icon.

### Cards / Containers
- **Corner Style:** 6px.
- **Background:** card surface.
- **Shadow Strategy:** flow card rest (see Elevation).
- **Border:** white at 9% idle, 20% on hover; emerald at 70% with a 25% ring when selected; amber at 45% when unfinished.
- **Internal Padding:** 0.75rem by 0.875rem, a 2rem icon tile (4px corners) at left in the step's tone, then title and detail.
- **Lists:** rows inside one bordered 6px container, divided by hairlines, 14px vertical padding, with a white-at-3% hover.

### Inputs / Fields
- **Style:** white at 4% fill, white-at-10% hairline, 2px corners, 0.75rem by 0.5rem padding (light: white fill, gray-200 border). Pickers use the same face as a button at min 2.5rem.
- **Focus:** border to emerald at 60% plus a 2px emerald ring at 30%.
- **Segmented choice:** a 2px-cornered track with a 4px inset; the chosen option is white at 10% and medium weight (light: white with a small shadow).
- **Disabled:** not-allowed cursor, text at 70%.

### Page Bar
Every page inside the app wears the same bar (`PageHeader`); pages do not draw their own.
- **Size:** 56px tall on a desktop, the height of the sidebar's logo row and the inbox's pane headers, so every top edge is one line. On a phone it wraps, and the actions take their own line under the title.
- **Surface:** the page colour at 95% with a backdrop blur and a white-at-8% bottom hairline (light: white at 95%, gray-200).
- **Left, in order:** a back arrow (2rem, ghost) only on a page you drill into: a record, an editor or a "new" page. Then the page's 1.25rem glyph at white 50%, the same glyph as its navigation item. Then the title (1.125rem semibold) with any status beside it, and one line under it.
- **The line under the title:** on a record page (whose title is the record's name) it is the trail up to it, like "Settings › Users", without repeating the page itself. On every other page it is one sentence saying what the page is for. It is truncated to one line on a desktop, with the full text on hover.
- **Back:** a page the navigation reaches has no back arrow. The arrow goes back where the person came from and falls back to the parent page. An editor with unsaved work takes it over to ask first.
- **Status:** a badge or pill beside the title (Active, a campaign's state, Cached, a rule's draft status), never among the buttons.
- **Actions, quiet to loud:** view controls (a date range, an account or agent picker), then outlined secondary actions, then at most one filled primary action, always last. A destructive action goes first, as a destructive outline. A ⋯ overflow menu, if there is one, closes the row. Every control in the bar is 2rem tall.
- **Editors:** the thing being edited names the page. Its name is typed in the title's place (`PageTitleInput`: the title's size, with a border only on hover or focus). Its settings go in the editor's own panel, not the bar.
- **A page's own views** (the automation builder's Build and History) sit in a tab row inside the bar.
- **The inbox** is the one exception. It is a workspace whose panes each carry a 56px header of their own on the same line.

### Navigation
- **Tabs:** 0.875rem text with a 2px underline; active is foreground, medium weight, emerald underline; idle is white at 55%.
- **Sidebar:** the active item carries a 3px emerald bar on its leading edge.

### Status Pill
A full pill with a 0.375rem dot and a 0.75rem medium label. It says the rule's state and, where there is something to do, is a button to it: a draft with problems (amber) opens the first problem with a trailing chevron; failing (red) opens History; On is a quiet emerald with no action.

### Flow Canvas (signature)
The page colour with a faint dot grid (22px gap, 1.5px dots at `flow-dots`), 1.5px edges at `flow-edge`, and a 1.5rem round + on every edge (neutral, emerald on hover, solid emerald while its menu is open). Hovering a card shows a small popover toolbar (duplicate, remove). A shown trace dims untaken cards and edges to 35%, then draws the walked path in emerald (2.25px) one stretch at a time in walk order: 240ms per stretch, 140ms stagger, ease `cubic-bezier(0.25, 1, 0.5, 1)`. Each trace mark fades up 200ms after its edge is drawn. Under prefers-reduced-motion, the path and marks appear at once.

### Rule Read-Back (signature)
"What this does" writes the whole rule as prose in muted text with chosen values in foreground medium weight. Each step's words are an inline link (a span with role=button, so it wraps with the sentence) with a dotted underline that opens its card. Branch paragraphs are indented behind a 2px hairline rule, 14px per level.

### Problems and Failures
Problems use the builder's own words: the step's title, then "Needs: team", linking to the card. Engine error strings are never shown in the interface; Run History keeps them only in a hover title.

## Do's and Don'ts

### Do:
- **Do** give each path hue one meaning: emerald for start, selection and the lit path; violet for a question; sky for a wait; amber only for unfinished; neutral for actions.
- **Do** write card titles as the sentence of what the step does, and build details from parts with chosen values in medium weight and foreground.
- **Do** size flow cards from their measured words (53 / 57 / 71px for 0 / 1 / 2 detail lines at the 14px rem), deriving every size from the root font.
- **Do** show variables as chips in fields and as ‹Label› in read-only text.
- **Do** make a status actionable when there is something to act on: draft with problems goes to the first problem, failing goes to History.
- **Do** keep every animation behind prefers-reduced-motion.
- **Do** take every corner from the 2px / 4px / 6px / full scale.

- **Do** give every page the shared page bar, a glyph and one line under its title; a page's main action is the filled button, last in the row.

### Don't:
- **Don't** hand-roll a page header, put a back arrow on a page the navigation reaches, or put a labelled "Name" field in a toolbar.
- **Don't** use amber for anything that is not "still needs finishing", and don't give the idle trigger card an emerald border.
- **Don't** let people drag or wire nodes; the canvas lays itself out and steps are added with +.
- **Don't** show `{{variable}}` syntax or raw engine error strings to the person building the rule.
- **Don't** put an eyebrow or kicker above a card title.
- **Don't** put a white label on the emerald primary; it measures 2.6:1.
- **Don't** paint links green by default; only a link inside prose uses the link style.
- **Don't** shrink a desktop canvas below 0.6 zoom, or open a phone canvas zoomed out.
