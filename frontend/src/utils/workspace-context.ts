// Query data stays in component memory and is discarded when the scope changes.
let generation = 0
let controller = new AbortController()
export const workspaceGeneration = () => generation
export const workspaceSignal = () => controller.signal
export function changeWorkspaceContext() {
  controller.abort()
  controller = new AbortController()
  generation++
}
