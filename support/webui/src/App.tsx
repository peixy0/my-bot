import { useCallback, useLayoutEffect, useRef, useState, type ChangeEvent, type KeyboardEvent } from 'react'
import { Lightbox } from './Lightbox'
import { Markdown } from './Markdown'
import type { ResponseMetadata } from './protocol'
import { defaultEndpoint } from './protocol'
import type { TimelineItem, ToolItem } from './chatReducer'
import { useChatSocket } from './useChatSocket'

type StagedImage = {
  dataUrl: string
  base64: string
  mimeType: string
  name: string
  size: number
}

const prompts = [
  ['Capabilities', 'What tools do you have access to? Give me a one-line summary of each.'],
  [
    'Workspace tour',
    "Give me a brief tour of this workspace. What's configured, and what should I know before asking deeper questions?"
  ],
  ['Draw a diagram', 'Draw a mermaid sequence diagram showing how you handle a user question from start to finish.'],
  ['Explain the loop', 'Explain how your agent loop works in 5 sentences or fewer.']
]

function formatBytes(bytes: number) {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

function formatMetadata(metadata?: ResponseMetadata) {
  if (!metadata) return ''
  const parts: string[] = []
  if (metadata.model?.trim()) parts.push(metadata.model.trim())
  if ((metadata.completion_tokens || 0) > 0 && (metadata.generation_seconds || 0) > 0) {
    parts.push(`${((metadata.completion_tokens || 0) / (metadata.generation_seconds || 1)).toFixed(1)} tokens/s`)
  }
  if ((metadata.total_tokens || 0) > 0) {
    const total = Number(metadata.total_tokens).toLocaleString('en-US')
    if ((metadata.context_window || 0) > 0) {
      const context = Number(metadata.context_window).toLocaleString('en-US')
      const percent = ((metadata.total_tokens || 0) * 100) / (metadata.context_window || 1)
      parts.push(`${total} / ${context} tokens (${percent.toFixed(1)}%)`)
    } else {
      parts.push(`${total} tokens`)
    }
  }
  return parts.join(' · ')
}

function summarizeArgs(tool: ToolItem) {
  const { name, args } = tool
  const text = (value: unknown) =>
    typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean' ? String(value) : ''
  if (name === 'grep') {
    const include = text(args.include)
    return `${JSON.stringify(text(args.pattern))}${include ? ` [${include}]` : ''}`
  }
  if (name === 'read_file') {
    const startLine = text(args.start_line)
    return `${text(args.filename) || text(args.path)}${startLine ? ` L${startLine}` : ''}`
  }
  if (name === 'glob') return text(args.glob_pattern) || text(args.pattern)
  if (name === 'agent') return text(args.task).slice(0, 80)
  if (name === 'fleet') return `${Array.isArray(args.tasks) ? args.tasks.length : 0} subagents`
  const key = Object.keys(args)[0]
  if (!key) return ''
  const value =
    typeof args[key] === 'string' ? JSON.stringify(String(args[key]).slice(0, 60)) : JSON.stringify(args[key])
  return `${key}=${value || ''}`.slice(0, 80)
}

function ToolEntry({ item }: { item: ToolItem }) {
  return (
    <article className={`tool-entry ${item.status}`} aria-label={`${item.name} ${item.status}`}>
      <div className="tool-head">
        <span className="tool-icon" aria-hidden="true">
          ↳
        </span>
        <strong>{item.name}</strong>
        <span className="tool-args" title={summarizeArgs(item)}>
          {summarizeArgs(item)}
        </span>
        <span className="tool-status">{item.status === 'running' ? 'running' : item.status}</span>
      </div>
      {item.reasoning && <div className="tool-reasoning">{item.reasoning}</div>}
      {item.preview && <pre className="tool-preview">{item.preview}</pre>}
    </article>
  )
}

function TimelineEntry({ item, openImage }: { item: TimelineItem; openImage: (src: string) => void }) {
  const [copied, setCopied] = useState(false)
  if (item.kind === 'session') {
    return (
      <div className="session-divider" role="separator">
        <span>Session {item.chatId}</span>
      </div>
    )
  }
  if (item.kind === 'tool') {
    return (
      <div className="timeline-row tool">
        <span className="avatar spacer" />
        <ToolEntry item={item} />
      </div>
    )
  }
  if (item.kind === 'image') {
    return (
      <div className={`timeline-row ${item.role}`}>
        {item.role === 'assistant' && <span className="avatar">BOT</span>}
        <div className="bubble image-bubble">
          <button className="image-open" type="button" onClick={() => openImage(item.src)} aria-label="Open image">
            <img src={item.src} alt={item.role === 'user' ? 'Uploaded image' : 'Agent image'} />
          </button>
          {item.caption && <div className="image-caption">{item.caption}</div>}
        </div>
        {item.role === 'user' && <span className="avatar user-avatar">YOU</span>}
      </div>
    )
  }
  if (item.role === 'system') {
    return (
      <div className="system-notice" role="status">
        {item.raw}
      </div>
    )
  }
  const metadata = formatMetadata(item.metadata)
  return (
    <div className={`timeline-row ${item.role}`}>
      {item.role === 'assistant' && <span className="avatar">BOT</span>}
      <article
        className={`bubble ${item.role === 'assistant' ? 'assistant-bubble' : 'user-bubble'}${item.final ? '' : ' streaming'}`}
      >
        {item.role === 'assistant' ? (
          <>
            <Markdown raw={item.raw} final={item.final} onOpenImage={openImage} />
            {item.final && (
              <button
                type="button"
                className={`copy-button${copied ? ' copied' : ''}`}
                aria-label="Copy response"
                onClick={() => {
                  void navigator.clipboard.writeText(item.raw).then(() => {
                    setCopied(true)
                    window.setTimeout(() => setCopied(false), 1800)
                  })
                }}
              >
                {copied ? '✓' : 'Copy'}
              </button>
            )}
            {metadata && <footer className="response-meta">{metadata}</footer>}
          </>
        ) : (
          item.raw
        )}
      </article>
      {item.role === 'user' && <span className="avatar user-avatar">YOU</span>}
    </div>
  )
}

export default function App() {
  const [initialEndpoint] = useState(() => defaultEndpoint())
  const [endpoint, setEndpoint] = useState(initialEndpoint)
  const { state, dispatch, connect, disconnect, send, nextMessageId } = useChatSocket(initialEndpoint)
  const [text, setText] = useState('')
  const [staged, setStaged] = useState<StagedImage | null>(null)
  const [lightbox, setLightbox] = useState<string | null>(null)
  const timelineRef = useRef<HTMLDivElement>(null)
  const stickRef = useRef(true)
  const connected = state.status === 'connected'
  const hasConversation = state.timeline.some(
    (item) => item.kind === 'image' || item.kind === 'tool' || (item.kind === 'text' && item.role !== 'system')
  )

  useLayoutEffect(() => {
    const timeline = timelineRef.current
    if (timeline && stickRef.current) timeline.scrollTop = timeline.scrollHeight
  }, [state.timeline])

  const sendAll = useCallback(() => {
    const message = text.trim()
    if (!connected || (!message && !staged)) return
    const messageId = nextMessageId()
    if (staged) {
      const sent = send({
        type: 'image',
        message_id: messageId,
        data: staged.base64,
        mime_type: staged.mimeType,
        message
      })
      if (!sent) return
      dispatch({ type: 'sent_image', src: staged.dataUrl, caption: message })
      setStaged(null)
    } else {
      const sent = send({ type: 'text', message, message_id: messageId })
      if (!sent) return
      dispatch({ type: 'sent_text', text: message })
    }
    setText('')
  }, [connected, dispatch, nextMessageId, send, staged, text])

  const selectFile = (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0]
    event.target.value = ''
    if (!file) return
    const reader = new FileReader()
    reader.onload = () => {
      if (typeof reader.result !== 'string') return
      setStaged({
        dataUrl: reader.result,
        base64: reader.result.split(',')[1] || '',
        mimeType: file.type || 'image/jpeg',
        name: file.name,
        size: file.size
      })
    }
    reader.readAsDataURL(file)
  }

  return (
    <main className="app-shell">
      <header className="topbar">
        <div className="brand">
          <span className="brand-name">my-bot</span>
          <span className="brand-context">web console</span>
        </div>
        <div className="connection-meta">
          <span className={`status-dot ${state.status}`} />
          <span className="status-text">{state.status[0].toUpperCase() + state.status.slice(1)}</span>
          {state.chatId && (
            <span className="chat-id" title={state.chatId}>
              {state.chatId}
            </span>
          )}
        </div>
        {connected ? (
          <button className="disconnect-button" type="button" onClick={disconnect}>
            Disconnect
          </button>
        ) : (
          <form
            className="endpoint-form"
            onSubmit={(event) => {
              event.preventDefault()
              connect(endpoint.trim())
            }}
          >
            <label className="sr-only" htmlFor="endpoint">
              WebSocket endpoint
            </label>
            <input
              id="endpoint"
              value={endpoint}
              onChange={(event) => setEndpoint(event.target.value)}
              spellCheck={false}
            />
            <button type="submit" disabled={state.status === 'connecting'}>
              {state.status === 'connecting' ? 'Connecting' : 'Connect'}
            </button>
          </form>
        )}
      </header>

      <div
        className="timeline"
        ref={timelineRef}
        onScroll={(event) => {
          const node = event.currentTarget
          stickRef.current = node.scrollHeight - node.clientHeight - node.scrollTop < 80
        }}
      >
        <div className="timeline-inner" aria-live="polite">
          {!hasConversation && (
            <section className="welcome">
              <p className="welcome-kicker">Local agent workspace</p>
              <h1>Start with a task.</h1>
              <p className="welcome-intro">
                Describe the outcome you need. The working trace stays visible as tools run.
              </p>
              <div className="prompt-list">
                {prompts.map(([label, prompt], index) => (
                  <button key={label} type="button" disabled={!connected} onClick={() => setText(prompt)}>
                    <span className="prompt-number">{String(index + 1).padStart(2, '0')}</span>
                    <span className="prompt-copy">
                      <strong>{label}</strong>
                      <span>{prompt}</span>
                    </span>
                    <span className="prompt-arrow" aria-hidden="true">
                      ↗
                    </span>
                  </button>
                ))}
              </div>
            </section>
          )}
          {state.timeline.map((item) => (
            <TimelineEntry key={item.id} item={item} openImage={setLightbox} />
          ))}
        </div>
      </div>

      {state.thinking && (
        <div className="thinking" role="status">
          <span className="thinking-dots">
            <i />
            <i />
            <i />
          </span>
          Working…
        </div>
      )}

      <footer className="composer-area">
        {staged && (
          <div className="image-preview">
            <img src={staged.dataUrl} alt="Selected upload preview" />
            <span>
              <strong>{staged.name}</strong>
              <small>{formatBytes(staged.size)}</small>
            </span>
            <button type="button" aria-label="Remove image" onClick={() => setStaged(null)}>
              ×
            </button>
          </div>
        )}
        <div className="composer">
          <label className={`attach-button${staged ? ' active' : ''}`} aria-label="Attach image">
            <input type="file" accept="image/*" disabled={!connected} onChange={selectFile} />
            <span aria-hidden="true">＋</span>
          </label>
          <textarea
            value={text}
            disabled={!connected}
            rows={1}
            aria-label="Message"
            placeholder={connected ? 'Describe a task or paste context…' : 'Connect to start working'}
            onChange={(event) => {
              setText(event.target.value)
              event.target.style.height = 'auto'
              event.target.style.height = `${Math.min(event.target.scrollHeight, 180)}px`
            }}
            onKeyDown={(event: KeyboardEvent<HTMLTextAreaElement>) => {
              if (event.key === 'Enter' && !event.shiftKey) {
                event.preventDefault()
                sendAll()
              }
            }}
          />
          <button
            className="send-button"
            type="button"
            disabled={!connected || (!text.trim() && !staged)}
            onClick={sendAll}
            aria-label="Send message"
          >
            <span aria-hidden="true">↑</span>
          </button>
        </div>
        <div className="composer-hint">Enter sends · Shift+Enter adds a line</div>
      </footer>
      <Lightbox src={lightbox} onClose={() => setLightbox(null)} />
    </main>
  )
}
