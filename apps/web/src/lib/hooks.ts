import { useEffect, useState } from 'react'

/** Debounce a fast-changing value (search inputs) so keystroke-driven
 * queries fire once per pause instead of once per character. */
export function useDebouncedValue<T>(value: T, delayMs = 250): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const t = setTimeout(() => setDebounced(value), delayMs)
    return () => clearTimeout(t)
  }, [value, delayMs])
  return debounced
}
