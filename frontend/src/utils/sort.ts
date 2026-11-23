export type OrderBy = "name" | "size" | "modified" | "created"

export interface SortState {
  orderBy: OrderBy
  reverse: boolean
}

export interface SortableObj {
  name: string
  size: number
  is_dir: boolean
  created?: string
  modified?: string
}

export const DEFAULT_SORT_STATE: SortState = {
  orderBy: "name",
  reverse: false,
}

const VALID_ORDER_BY = new Set<string>(["name", "size", "modified", "created"])

export const isValidOrderBy = (val: unknown): val is OrderBy =>
  typeof val === "string" && VALID_ORDER_BY.has(val)

export const defaultReverseFor = (orderBy: OrderBy): boolean => {
  switch (orderBy) {
    case "size":
    case "created":
    case "modified":
      return true
    case "name":
    default:
      return false
  }
}

export const sortArrowFor = (orderBy: OrderBy, reverse: boolean): "↑" | "↓" =>
  reverse === defaultReverseFor(orderBy) ? "↑" : "↓"

export const nextSortState = (
  current: SortState,
  target: OrderBy,
): SortState => {
  if (current.orderBy === target) {
    return { orderBy: target, reverse: !current.reverse }
  }
  return { orderBy: target, reverse: defaultReverseFor(target) }
}

const nameCollator = new Intl.Collator("zh-CN", {
  numeric: true,
  sensitivity: "base",
})

const parseTimestamp = (primary?: string, fallback?: string): number => {
  const tryParse = (raw?: string): number | null => {
    if (!raw) return null
    const ms = Date.parse(raw)
    if (!Number.isFinite(ms) || ms <= 0) return null
    return ms
  }
  return tryParse(primary) ?? tryParse(fallback) ?? 0
}

export const compareNames = (a: string, b: string): number => {
  const cmp = nameCollator.compare(a, b)
  if (cmp !== 0) return cmp
  return a < b ? -1 : a > b ? 1 : 0
}

export const compareObjs = (
  a: SortableObj,
  b: SortableObj,
  orderBy: OrderBy,
  reverse = false,
): number => {
  if (a.is_dir !== b.is_dir) {
    return a.is_dir ? -1 : 1
  }

  let cmp = 0
  switch (orderBy) {
    case "created":
      cmp =
        parseTimestamp(a.created, a.modified) -
        parseTimestamp(b.created, b.modified)
      break
    case "modified":
      cmp =
        parseTimestamp(a.modified, a.created) -
        parseTimestamp(b.modified, b.created)
      break
    case "size":
      cmp = (a.size || 0) - (b.size || 0)
      break
    case "name":
    default:
      cmp = compareNames(a.name, b.name)
      break
  }

  if (cmp !== 0) {
    return reverse ? -cmp : cmp
  }
  return compareNames(a.name, b.name)
}
