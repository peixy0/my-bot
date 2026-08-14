import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import App from './App'

class MockWebSocket {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSING = 2
  static CLOSED = 3
  static instances: MockWebSocket[] = []

  readonly url: string
  readyState = MockWebSocket.CONNECTING
  onmessage: ((event: MessageEvent<string>) => void) | null = null
  onerror: (() => void) | null = null
  onclose: (() => void) | null = null
  send = vi.fn<(data: string) => void>()

  constructor(url: string) {
    this.url = url
    MockWebSocket.instances.push(this)
  }

  serverMessage(frame: unknown) {
    this.onmessage?.(new MessageEvent('message', { data: JSON.stringify(frame) }))
  }

  close() {
    this.readyState = MockWebSocket.CLOSED
    this.onclose?.()
  }
}

function sentFrame(socket: MockWebSocket) {
  const raw = socket.send.mock.calls[0]?.[0]
  if (typeof raw !== 'string') throw new Error('expected a sent WebSocket frame')
  const parsed: unknown = JSON.parse(raw)
  if (!parsed || typeof parsed !== 'object') throw new Error('expected an object WebSocket frame')
  return parsed as Record<string, unknown>
}

describe('App connection and sending', () => {
  beforeEach(() => {
    MockWebSocket.instances = []
    vi.stubGlobal('WebSocket', MockWebSocket)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('auto-connects and sends protocol-compatible text', async () => {
    const user = userEvent.setup()
    render(<App />)
    const socket = MockWebSocket.instances[0]

    expect(socket.url).toBe('ws://localhost:3000/api/bot')
    socket.readyState = MockWebSocket.OPEN
    act(() => socket.serverMessage({ type: 'connected', chat_id: 'chat-123' }))

    const composer = await screen.findByRole('textbox', { name: 'Message' })
    await user.type(composer, 'hello agent')
    fireEvent.keyDown(composer, { key: 'Enter' })

    expect(socket.send).toHaveBeenCalledOnce()
    const payload = sentFrame(socket)
    expect(payload).toMatchObject({
      type: 'text',
      message: 'hello agent'
    })
    expect(payload.message_id).toMatch(/^ui-\d+-1$/)
    expect(screen.getByText('hello agent')).toBeInTheDocument()
  })

  it('keeps history across an explicit reconnect', async () => {
    const user = userEvent.setup()
    render(<App />)
    const first = MockWebSocket.instances[0]
    first.readyState = MockWebSocket.OPEN
    act(() => {
      first.serverMessage({ type: 'connected', chat_id: 'session-one' })
      first.serverMessage({ type: 'message', text: 'persistent reply' })
    })

    await user.click(await screen.findByRole('button', { name: 'Disconnect' }))
    expect(await screen.findByText('Disconnected.')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Connect' }))

    const second = MockWebSocket.instances[1]
    second.readyState = MockWebSocket.OPEN
    act(() => second.serverMessage({ type: 'connected', chat_id: 'session-two' }))

    await waitFor(() => expect(screen.getByText('Session session-two')).toBeInTheDocument())
    expect(screen.getByText('persistent reply')).toBeInTheDocument()
    expect(screen.getByText('Session session-one')).toBeInTheDocument()
  })

  it('sends an image with an optional caption', async () => {
    const user = userEvent.setup()
    const { container } = render(<App />)
    const socket = MockWebSocket.instances[0]
    socket.readyState = MockWebSocket.OPEN
    act(() => socket.serverMessage({ type: 'connected', chat_id: 'image-session' }))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')
    expect(fileInput).not.toBeNull()

    await user.upload(fileInput!, new File(['pixels'], 'photo.png', { type: 'image/png' }))
    await screen.findByAltText('Selected upload preview')
    await user.type(screen.getByRole('textbox', { name: 'Message' }), 'caption')
    await user.click(screen.getByRole('button', { name: 'Send message' }))

    const payload = sentFrame(socket)
    expect(payload).toMatchObject({
      type: 'image',
      message: 'caption',
      mime_type: 'image/png'
    })
    expect(payload.data).toBeTruthy()
    expect(screen.getByAltText('Uploaded image')).toBeInTheDocument()
  })
})
