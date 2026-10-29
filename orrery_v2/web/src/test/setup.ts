process.env.TZ = 'Asia/Shanghai'

class MemoryStorage {
  private readonly items = new Map<string, string>()

  get length() {
    return this.items.size
  }

  clear() {
    this.items.clear()
  }

  getItem(key: string) {
    return this.items.get(String(key)) ?? null
  }

  key(index: number) {
    return Array.from(this.items.keys())[index] ?? null
  }

  removeItem(key: string) {
    this.items.delete(String(key))
  }

  setItem(key: string, value: string) {
    this.items.set(String(key), String(value))
  }
}

const storage = globalThis.localStorage
if (!storage || typeof storage.getItem !== 'function' || typeof storage.removeItem !== 'function') {
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: new MemoryStorage(),
    writable: true
  })
}
