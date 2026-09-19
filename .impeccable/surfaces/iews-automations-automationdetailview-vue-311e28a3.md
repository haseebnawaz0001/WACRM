---
version: 1
slug: "iews-automations-automationdetailview-vue-311e28a3"
primary_target: "frontend/src/views/automations/AutomationDetailView.vue"
related_targets: ["frontend/src/views/automations/AutomationsView.vue","frontend/src/components/crmactions/CrmActionCard.vue"]
---

# Automations — surface brief

## Scope and mode

- Mode: **Operate**. The visitor builds, tests and runs rules that act on customers.
- Surfaces:
  - the automations list (`/automations`)
  - the "start from a goal" gallery
  - the builder (`/automations/:id`) with its canvas and step inspector
  - the test run, and run history
  - the shared action editor (`components/crmactions/*`), which the chatbot CRM-action node and keyword rules also use and which must keep working there
- Inherits the established app look: near-black and light themes, neutral greys, one emerald accent, Inter, 6px corners. There is no new visual identity.

## Audience, job, constraints

- Built mostly by front-desk and ops staff who know the process but not software vocabulary (confirmed). Admins and technical staff use it too.
- The job: turn "whenever X happens to a customer, do Y (unless Z; if so, then W; wait a day, then V)" into a rule they trust enough to switch on.
- The engine gains two flow-control steps: **if / else**, a question about the contact that splits the path, and **wait**, a pause before the next step.
- Real limits hold: sends obey the 24-hour window, template approval and opt-outs; loop depth; per-rule run policies.
- Six locales including RTL. Keyboard operable. Desktop first; phones must stay usable, since the inspector becomes a sheet.

## Direction contract

THESIS: An automation is the path a customer walks, drawn top to bottom. Its first card says what starts it, each card below is one plain sentence, and it splits into Yes and No wherever it asks a question. You build by pressing + on the path and filling the blanks with pickers, never by dragging or wiring nodes. It refuses the flat settings form and the free-drag node editor.

OWN-WORLD: The app's own materials. The canvas is the page background with a faint dot grid. Cards use the card surface with a 1px border, 6px radius and Inter. Emerald marks only the live path, insertion points, selection and primary actions. Each step kind has a small glyph tile: emerald for the trigger, amber for if / else, sky for wait, neutral with its own icon for actions. Card bodies are written sentences with the filled-in values in weight, not form fields.

STORY: A person sees the whole rule as one picture and one sentence at once. They click a card to answer its questions in plain words, add a step anywhere with +, try the rule on a real contact and watch the path it would take light up, then switch it on with confidence.

FIRST VIEWPORT:
- **Top bar:** back, editable name, a status pill (Draft, On, Off, or Needs attention with the reason), Test, and a primary Turn on / Save.
- **Canvas (left, about two thirds):** the start card at the top centre, the path descending through its steps to an End marker, and + on every connector.
- **Inspector (right):** the selected card's questions, or a "What this does" plain-English summary with health when nothing is selected.
- **Tabs (above the canvas):** Build | History.

FORM: an auto-laid-out vertical flow canvas. The user pinned this form in the interview, so no roll was run and there is no seed key. Signature interaction: **Try it on a contact.** A dry run lights the path that contact would take, shows faded branches not taken, and puts a result chip on each step.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

## Unresolved

- Whether waits show a live count of contacts currently waiting at that step. This needs a count endpoint and is in scope if cheap.
