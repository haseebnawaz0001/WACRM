import { ref } from 'vue'

/**
 * Destinations a person has pinned to the top of the sidebar.
 *
 * The default navigation is complete on its own — this is not the only way to
 * reach anything, which is what keeps a new account from opening on an empty
 * rail. It exists because the right menu differs per person: a campaign
 * manager lives in Campaigns and Templates, a supervisor in Agent analytics,
 * and neither should have to open a group twenty times a day to reach the one
 * page that is their job.
 *
 * Paths, not items: a pin has to survive a rename, an icon change, or a
 * permission being revoked. A pinned path the viewer can no longer open is
 * simply not rendered.
 */
const STORAGE_KEY = 'sidebar-pins'

function read(): string[] {
  try {
    const raw = JSON.parse(localStorage.getItem(STORAGE_KEY) || '[]')
    return Array.isArray(raw) ? raw.filter((p): p is string => typeof p === 'string') : []
  } catch {
    return []
  }
}

const pins = ref<string[]>(read())

function persist() {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(pins.value))
  } catch {
    // Storage unavailable (private mode); pins last for this session only.
  }
}

export function useNavPins() {
  return {
    pins,
    isPinned: (path: string) => pins.value.includes(path),
    toggle: (path: string) => {
      const at = pins.value.indexOf(path)
      if (at >= 0) {
        pins.value.splice(at, 1)
      } else {
        // Newest last, so the list does not reshuffle under the cursor of
        // somebody pinning two things in a row.
        pins.value.push(path)
      }
      persist()
    }
  }
}
