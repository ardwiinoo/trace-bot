const API_URL = import.meta.env.VITE_API_URL ?? 'http://localhost:8081'
const API_TOKEN = import.meta.env.VITE_API_TOKEN as string | undefined

export interface Query {
  tool: string
  args: string
}

export interface ChatResponse {
  answer: string
  queries: Query[] | null
}

async function post<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(`${API_URL}${path}`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      ...(API_TOKEN ? { Authorization: `Bearer ${API_TOKEN}` } : {}),
    },
    body: JSON.stringify(body),
  })
  if (res.status === 204) return undefined as T
  const text = await res.text()
  if (!res.ok) {
    let msg = text
    try {
      msg = JSON.parse(text).error ?? text
    } catch {
      // body was not JSON; show it as is
    }
    throw new Error(msg || `HTTP ${res.status}`)
  }
  return JSON.parse(text) as T
}

export const chat = (sessionId: string, message: string) =>
  post<ChatResponse>('/api/chat', { session_id: sessionId, message })

export const reset = (sessionId: string) => post<void>('/api/reset', { session_id: sessionId })
