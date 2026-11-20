/**
 * Normalizes the build-time version for display. Local and test builds have no
 * VITE_APP_VERSION, so they report "dev" instead of a misleading release number.
 */
export function displayVersion(value: string | undefined): string {
  const trimmed = value?.trim()
  if (!trimmed) return 'dev'
  return trimmed.startsWith('v') ? trimmed.slice(1) : trimmed
}
