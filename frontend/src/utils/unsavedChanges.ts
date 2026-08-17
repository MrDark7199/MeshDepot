import { createSignal } from 'solid-js'

// At most one form is edited at a time, so a module-level singleton is correct.

const [isDirty, setIsDirty] = createSignal(false)
const [pendingAction, setPendingAction] = createSignal<(() => void) | null>(null)
const [pendingMessage, setPendingMessage] = createSignal('')

// Optional override: when set, replaces the simple isDirty boolean with a
// real diff check (used by DesignPage to compare current form to initial snapshot).
let _shouldBlockFn: (() => boolean) | null = null

function isActuallyDirty(): boolean {
  return _shouldBlockFn ? _shouldBlockFn() : isDirty()
}

// Register the beforeunload guard once - it reads live state on every event.
window.addEventListener('beforeunload', (e: BeforeUnloadEvent) => {
  if (!isActuallyDirty()) return
  e.preventDefault()
  e.returnValue = ''
})


export const pendingActionSignal = pendingAction
export const pendingMessageSignal = pendingMessage

export function markDirty() { setIsDirty(true) }

/** Reads the current effective dirty state (honours any setShouldBlock override). */
export function isFormDirty(): boolean { return isActuallyDirty() }

export function resetDirty() {
  setIsDirty(false)
  _shouldBlockFn = null
}

/** Replace the simple isDirty flag with a function that computes the real
 *  dirty state (e.g. JSON snapshot comparison). Pass null to clear. */
export function setShouldBlock(fn: (() => boolean) | null) {
  _shouldBlockFn = fn
  if (fn) setIsDirty(true) // activate beforeunload guard
}

/** Show the in-app confirm modal if the form is dirty, otherwise run action. */
export function guardClose(message: string, action: () => void) {
  if (!isActuallyDirty()) { setIsDirty(false); _shouldBlockFn = null; action(); return }
  setPendingMessage(message)
  setPendingAction(() => action)
}

/** Called by the "Discard" button in the confirm modal. */
export function confirmDiscard() {
  setIsDirty(false)
  _shouldBlockFn = null
  const action = pendingAction()
  setPendingAction(null)
  action?.()
}

/** Called by the "Cancel" button in the confirm modal. */
export function cancelDiscard() {
  setPendingAction(null)
}


/** Use inside a SolidJS component to get the guard helpers bound to a message. */
export function useUnsavedChanges(confirmMessage: string) {
  return {
    isDirty,
    markDirty,
    resetDirty,
    setShouldBlock,
    guardClose: (action: () => void) => guardClose(confirmMessage, action),
  }
}
