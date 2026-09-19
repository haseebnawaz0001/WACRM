/**
 * The app's field chrome, for the few controls that must be native elements
 * (a textarea whose caret a variable is inserted at). Copied from ui/input so
 * a native field and a component field are indistinguishable side by side.
 */
export const FIELD =
  'w-full rounded-sm border border-white/[0.1] bg-white/[0.04] px-3 py-2 text-sm text-white transition-colors duration-150 placeholder:text-white/40 hover:border-white/20 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500/30 focus-visible:border-emerald-500/60 disabled:cursor-not-allowed disabled:text-white/70 light:bg-white light:border-gray-200 light:text-gray-900 light:placeholder:text-gray-400 light:hover:border-gray-300 light:focus-visible:ring-emerald-500 light:focus-visible:border-emerald-500'

/**
 * The same chrome around an editable area that is not itself the focusable
 * element's box — the chip field, whose text scrolls beside its button.
 */
export const FIELD_SHELL =
  'w-full rounded-sm border border-white/[0.1] bg-white/[0.04] text-sm text-white transition-colors duration-150 hover:border-white/20 focus-within:ring-2 focus-within:ring-emerald-500/30 focus-within:border-emerald-500/60 light:bg-white light:border-gray-200 light:text-gray-900 light:hover:border-gray-300 light:focus-within:ring-emerald-500 light:focus-within:border-emerald-500'

/** A field that is a button (a picker's face). */
export const FIELD_BUTTON =
  'flex min-h-10 w-full items-center gap-2 rounded-sm border border-white/[0.1] bg-white/[0.04] px-3 py-1.5 text-left text-sm text-white transition-colors duration-150 hover:border-white/20 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500/30 focus-visible:border-emerald-500/60 disabled:cursor-not-allowed light:bg-white light:border-gray-200 light:text-gray-900 light:hover:border-gray-300'

export const PLACEHOLDER = 'text-white/40 light:text-gray-400'
