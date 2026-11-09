import { beforeEach } from 'vitest'

// Node's global localStorage can shadow happy-dom with undefined (Frontend CI,
// PLAT-746). Use the environment's real Storage so prototype refusal tests still work.
class MemoryStorage {
  private values = new Map<string, string>()
  get length() { return this.values.size }
  clear() { this.values.clear() }
  getItem(key: string) { return this.values.get(String(key)) ?? null }
  key(index: number) { return [...this.values.keys()][index] ?? null }
  removeItem(key: string) { this.values.delete(String(key)) }
  setItem(key: string, value: string) { this.values.set(String(key), String(value)) }
}

const StorageType = typeof window !== 'undefined' ? window.Storage : MemoryStorage
const storage = new StorageType()
Object.defineProperty(globalThis, 'Storage', { configurable: true, value: StorageType })
Object.defineProperty(globalThis, 'localStorage', { configurable: true, writable: true, value: storage })
if (typeof window !== 'undefined') Object.defineProperty(window, 'localStorage', { configurable: true, value: storage })
beforeEach(() => storage.clear())
