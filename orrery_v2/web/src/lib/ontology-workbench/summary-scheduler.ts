type SummaryTask = () => void

class OntologySummaryScheduler {
  private readonly limit: number
  private active = 0
  private queue: SummaryTask[] = []

  constructor(limit: number) {
    this.limit = Math.max(1, limit)
  }

  run<T>(signal: AbortSignal, task: () => Promise<T>): Promise<T> {
    if (signal.aborted) return Promise.reject(abortError())
    return new Promise<T>((resolve, reject) => {
      let started = false
      const start = () => {
        started = true
        signal.removeEventListener('abort', onAbort)
        this.active++
        void Promise.resolve().then(task).then(resolve, reject).finally(() => {
          this.active--
          this.drain()
        })
      }
      const onAbort = () => {
        if (started) return
        const index = this.queue.indexOf(start)
        if (index >= 0) this.queue.splice(index, 1)
        reject(abortError())
      }
      signal.addEventListener('abort', onAbort, { once: true })
      this.queue.push(start)
      this.drain()
    })
  }

  private drain() {
    while (this.active < this.limit && this.queue.length > 0) {
      this.queue.shift()?.()
    }
  }
}

function abortError() {
  const error = new Error('Ontology summary query was aborted')
  error.name = 'AbortError'
  return error
}

export const ontologySummaryConcurrency = 1

const ontologySummaryScheduler = new OntologySummaryScheduler(ontologySummaryConcurrency)

export function runOntologySummaryQuery<T>(signal: AbortSignal, task: () => Promise<T>) {
  return ontologySummaryScheduler.run(signal, task)
}

export { OntologySummaryScheduler }
