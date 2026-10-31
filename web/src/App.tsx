import { useEffect, useRef, useState } from 'react'
import { chat, reset, type Query } from './api'
import './App.css'

interface Message {
  role: 'user' | 'bot' | 'error'
  text: string
  queries?: Query[]
}

const SUGGESTIONS = [
  'Ada error di 30 menit terakhir?',
  'Jam berapa request paling tinggi dalam 1 jam terakhir?',
  'Endpoint mana yang paling banyak error?',
  'Berapa p95 latency per endpoint?',
]

function newSessionId(): string {
  return crypto.randomUUID()
}

function loadSessionId(): string {
  try {
    const saved = localStorage.getItem('trace-bot-session')
    if (saved) return saved
    const id = newSessionId()
    localStorage.setItem('trace-bot-session', id)
    return id
  } catch {
    return newSessionId()
  }
}

function prettyArgs(args: string): string {
  try {
    return JSON.stringify(JSON.parse(args), null, 2)
  } catch {
    return args
  }
}

export default function App() {
  const [sessionId, setSessionId] = useState(loadSessionId)
  const [messages, setMessages] = useState<Message[]>([])
  const [input, setInput] = useState('')
  const [busy, setBusy] = useState(false)
  const bottomRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, busy])

  async function send(text: string) {
    const question = text.trim()
    if (!question || busy) return
    setInput('')
    setMessages((m) => [...m, { role: 'user', text: question }])
    setBusy(true)
    try {
      const res = await chat(sessionId, question)
      setMessages((m) => [...m, { role: 'bot', text: res.answer, queries: res.queries ?? [] }])
    } catch (err) {
      setMessages((m) => [...m, { role: 'error', text: err instanceof Error ? err.message : String(err) }])
    } finally {
      setBusy(false)
    }
  }

  async function startOver() {
    const old = sessionId
    const id = newSessionId()
    try {
      localStorage.setItem('trace-bot-session', id)
    } catch {
      // storage unavailable; the new id just lives in memory
    }
    setSessionId(id)
    setMessages([])
    reset(old).catch(() => {}) // best effort: frees server-side history
  }

  return (
    <div className="app">
      <header>
        <h1>Trace Bot</h1>
        <button className="ghost" onClick={startOver} disabled={busy}>
          Percakapan baru
        </button>
      </header>

      <main>
        {messages.length === 0 && (
          <div className="empty">
            <p>Tanya apa saja soal log dan metrics service kamu.</p>
            <div className="chips">
              {SUGGESTIONS.map((s) => (
                <button key={s} className="chip" onClick={() => send(s)}>
                  {s}
                </button>
              ))}
            </div>
          </div>
        )}

        {messages.map((m, i) => (
          <div key={i} className={`msg ${m.role}`}>
            <div className="bubble">{m.text}</div>
            {m.queries && m.queries.length > 0 && (
              <details>
                <summary>{m.queries.length} query dijalankan</summary>
                {m.queries.map((q, j) => (
                  <pre key={j}>
                    <strong>{q.tool}</strong>
                    {'\n'}
                    {prettyArgs(q.args)}
                  </pre>
                ))}
              </details>
            )}
          </div>
        ))}

        {busy && (
          <div className="msg bot">
            <div className="bubble thinking">Menganalisis log dan metrics…</div>
          </div>
        )}
        <div ref={bottomRef} />
      </main>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          send(input)
        }}
      >
        <textarea
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.shiftKey) {
              e.preventDefault()
              send(input)
            }
          }}
          placeholder="Contoh: ada error jam 2 pagi tadi?"
          rows={1}
          disabled={busy}
        />
        <button type="submit" disabled={busy || !input.trim()}>
          Kirim
        </button>
      </form>
    </div>
  )
}
