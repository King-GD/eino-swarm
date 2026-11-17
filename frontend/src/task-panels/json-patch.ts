import type { JsonPatchOperation } from "./types"

function clone<T>(value: T): T { return JSON.parse(JSON.stringify(value)) as T }
function tokens(path: string): string[] {
  if (path === "") return []
  if (!path.startsWith("/") || /~(?:[^01]|$)/.test(path)) throw new Error("Invalid JSON Pointer")
  return path.slice(1).split("/").map((part) => part.replaceAll("~1", "/").replaceAll("~0", "~"))
}
function member(value: unknown, key: string): unknown {
  if (value === null || typeof value !== "object" || !Object.hasOwn(value, key)) throw new Error("Missing patch path")
  return (value as Record<string, unknown>)[key]
}
function read(value: unknown, path: string[]): unknown {
  return path.reduce((current, key) => member(current, key), value)
}
function equal(a: unknown, b: unknown): boolean {
  if (a === b) return true
  if (a === null || b === null || typeof a !== "object" || typeof b !== "object" || Array.isArray(a) !== Array.isArray(b)) return false
  const ak = Object.keys(a), bk = Object.keys(b)
  return ak.length === bk.length && ak.every((key) => Object.hasOwn(b, key) && equal(member(a, key), member(b, key)))
}
function write(root: unknown, path: string[], op: "add" | "remove" | "replace", value?: unknown): unknown {
  if (!path.length) {
    if (op === "remove") throw new Error("Panel state must remain JSON")
    return clone(value)
  }
  const parent = read(root, path.slice(0, -1)), key = path.at(-1)!
  if (parent === null || typeof parent !== "object") throw new Error("Invalid patch parent")
  if (Array.isArray(parent)) {
    const index = key === "-" && op === "add" ? parent.length : /^(0|[1-9]\d*)$/.test(key) ? Number(key) : -1
    if (index < 0 || index > parent.length || (op !== "add" && index === parent.length)) throw new Error("Invalid patch index")
    parent.splice(index, op === "add" ? 0 : 1, ...(op === "remove" ? [] : [clone(value)]))
  } else {
    if (op !== "add" && !Object.hasOwn(parent, key)) throw new Error("Missing patch path")
    if (op === "remove") delete (parent as Record<string, unknown>)[key]
    else Object.defineProperty(parent, key, { value: clone(value), configurable: true, enumerable: true, writable: true })
  }
  return root
}

/** Apply atomically: a rejected operation never mutates the last good state. */
export function applyJsonPatch(state: unknown, operations: JsonPatchOperation[]): unknown {
  let next = clone(state)
  for (const operation of operations) {
    const path = tokens(operation.path)
    if (operation.op === "test") {
      if (!equal(read(next, path), operation.value)) throw new Error("Patch test failed")
    } else if (operation.op === "move" || operation.op === "copy") {
      if (typeof operation.from !== "string") throw new Error("Missing patch source")
      const from = tokens(operation.from)
      if (operation.op === "move" && path.length > from.length && from.every((part, i) => part === path[i])) throw new Error("Cannot move into descendant")
      const value = clone(read(next, from))
      if (operation.op === "move" && operation.from === operation.path) continue
      if (operation.op === "move") next = write(next, from, "remove")
      next = write(next, path, "add", value)
    } else if (operation.op === "add" || operation.op === "replace" || operation.op === "remove") {
      if (operation.op !== "remove" && !Object.hasOwn(operation, "value")) throw new Error("Missing patch value")
      next = write(next, path, operation.op, operation.value)
    } else throw new Error("Unsupported patch operation")
  }
  return next
}
