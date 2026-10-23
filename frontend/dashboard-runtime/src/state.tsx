import React, { createContext, useContext, useState, useSyncExternalStore, useCallback, useEffect, useRef, type ReactNode } from 'react'

export type BaseComponentProps<P> = { props: P; children?: ReactNode; slots?: Record<string, ReactNode>; bindings?: Record<string, string>; emit: (event: string) => void; on: (event: string) => { emit: () => void; bound: boolean; shouldPreventDefault: boolean } }
type Check = { type: string; message: string; args?: Record<string, any> }
type Validation = { checks: Check[]; validateOn?: string }
function parts(path: string) {
  const keys = path.split('/').slice(1).map(key => key.replace(/~1/g, '/').replace(/~0/g, '~'))
  if (keys.some(key => ['__proto__', 'constructor', 'prototype'].includes(key))) throw Error('Invalid state path')
  return keys
}
export function getPath(value: any, path: string): any { return parts(path).reduce((item, key) => item?.[key], value) }
function assign(value: any, keys: string[], next: any): any {
  if (!keys.length) return next
  const [key, ...tail] = keys
  const copy = Array.isArray(value) ? [...value] : { ...value }
  copy[key as any] = assign(value?.[key], tail, next)
  return copy
}
export function createDashboardStore(initial: Record<string, any> = {}) {
  let state = initial
  const listeners = new Set<() => void>()
  const fields = new Map<string, Validation>()
  const validations = new Map<string, (value?: any) => boolean>()
  const store = {
    getSnapshot: () => state,
    get: (path: string) => getPath(state, path),
    set(path: string, value: any) { state = assign(state, parts(path), value); listeners.forEach(listener => listener()) },
    update(values: Record<string, any>) { for (const [path, value] of Object.entries(values)) state = assign(state, parts(path), value); listeners.forEach(listener => listener()) },
    subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener) } },
    fields, validations,
    validateAll() { let valid = true; for (const validate of validations.values()) valid = validate() && valid; return valid },
  }
  return store
}
const Context = createContext<ReturnType<typeof createDashboardStore> | null>(null)
export function DashboardStateProvider({ children, store }: { children: ReactNode; store: ReturnType<typeof createDashboardStore> }) { return <Context.Provider value={store}>{children}</Context.Provider> }
export function useDashboardStore() {
  const store = useContext(Context)
  if (!store) throw Error('Dashboard state must be used inside the hosted dashboard')
  useSyncExternalStore(store.subscribe, store.getSnapshot, store.getSnapshot)
  return store
}
export function useStateBinding<T>(path: string): [T | undefined, (value: T) => void] {
  const store = useDashboardStore()
  return [path ? store.get(path) : undefined, useCallback((value: T) => { if (path) store.set(path, value) }, [path, store])]
}
export { useStateBinding as useDashboardBinding }
export function useBoundProp<T>(value: T | undefined, path?: string): [T | undefined, (value: T) => void] {
  const [bound, set] = useStateBinding<T>(path ?? '')
  return [path ? bound : value, set]
}
function required(value: any) { return value != null && (typeof value === 'string' ? value.trim().length > 0 : Array.isArray(value) ? value.length > 0 : true) }
function checkValue(value: any, type: string, args: Record<string, any>): boolean {
  switch (type) {
    case 'required': return required(value)
    case 'requiredIf': return !args.field || required(value)
    case 'email': return typeof value === 'string' && /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value)
    case 'minLength': return typeof value === 'string' && value.length >= args.min
    case 'maxLength': return typeof value === 'string' && value.length <= args.max
    case 'min': return typeof value === 'number' && value >= args.min
    case 'max': return typeof value === 'number' && value <= args.max
    case 'numeric': return typeof value === 'number' ? !isNaN(value) : typeof value === 'string' && value.trim() !== '' && !isNaN(Number(value))
    case 'pattern': try { return typeof value === 'string' && new RegExp(args.pattern).test(value) } catch { return false }
    case 'url': try { new URL(value); return true } catch { return false }
    case 'matches': case 'equalTo': return value === args.other
    case 'lessThan': case 'greaterThan': {
      if (value == null || args.other == null || value === '' || args.other === '') return false
      const a = typeof value === typeof args.other ? value : Number(value)
      const b = typeof value === typeof args.other ? args.other : Number(args.other)
      return type === 'lessThan' ? a < b : a > b
    }
    default: return true
  }
}
export function useFieldValidation(path: string, config?: Validation) {
  const store = useDashboardStore()
  const [errors, setErrors] = useState<string[]>([])
  const latest = useRef(config); latest.current = config
  const validate = useCallback(() => {
    const errors = (latest.current?.checks ?? []).filter(check => {
      const args = Object.fromEntries(Object.entries(check.args ?? {}).map(([key, value]) => [key, value && typeof value === 'object' && '$state' in value ? store.get(value.$state) : value]))
      return !checkValue(store.get(path), check.type, args)
    }).map(check => check.message)
    setErrors(errors); return errors.length === 0
  }, [path, store])
  useEffect(() => {
    if (!path || !config) return
    store.fields.set(path, config); store.validations.set(path, validate)
    return () => { store.fields.delete(path); store.validations.delete(path) }
  }, [path, Boolean(config), store, validate])
  return { errors, validate }
}
