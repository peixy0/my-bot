import type { InboundFrame, ResponseMetadata } from './protocol'
import { streamKey } from './protocol'

export type ConnectionStatus = 'connecting' | 'connected' | 'disconnected'

type TimelineBase = { id: string }
export type TextItem = TimelineBase & {
  kind: 'text'
  role: 'user' | 'assistant' | 'system'
  raw: string
  final: boolean
  streamKey?: string
  metadata?: ResponseMetadata
}
export type ImageItem = TimelineBase & {
  kind: 'image'
  role: 'user' | 'assistant'
  src: string
  caption: string
}
export type ToolItem = TimelineBase & {
  kind: 'tool'
  toolCallId: string
  name: string
  args: Record<string, unknown>
  reasoning?: string
  status: 'running' | 'done' | 'error' | 'cancelled'
  preview?: string
}
export type SessionItem = TimelineBase & { kind: 'session'; chatId: string }
export type TimelineItem = TextItem | ImageItem | ToolItem | SessionItem
type NewTimelineItem = TimelineItem extends infer Item ? (Item extends TimelineItem ? Omit<Item, 'id'> : never) : never

export type ChatState = {
  status: ConnectionStatus
  chatId: string | null
  thinking: boolean
  timeline: TimelineItem[]
  sequence: number
}

export type ChatAction =
  | { type: 'connecting' }
  | { type: 'connection_failed'; message: string }
  | { type: 'closed' }
  | { type: 'socket_error' }
  | { type: 'frame'; frame: InboundFrame }
  | { type: 'sent_text'; text: string }
  | { type: 'sent_image'; src: string; caption: string }

export const initialChatState: ChatState = {
  status: 'disconnected',
  chatId: null,
  thinking: false,
  timeline: [],
  sequence: 0
}

function append(state: ChatState, item: NewTimelineItem): ChatState {
  const sequence = state.sequence + 1
  return { ...state, sequence, timeline: [...state.timeline, { ...item, id: `item-${sequence}` }] }
}

function finishStreamsAndTools(state: ChatState) {
  return state.timeline
    .filter((item) => item.kind !== 'text' || item.final || item.raw.trim())
    .map((item) => {
      if (item.kind === 'text' && !item.final) return { ...item, final: true }
      if (item.kind === 'tool' && item.status === 'running') {
        return { ...item, status: 'cancelled' as const, preview: 'cancelled' }
      }
      return item
    })
}

function reduceFrame(state: ChatState, frame: InboundFrame): ChatState {
  if (frame.type === 'connected') {
    return append({ ...state, status: 'connected', chatId: frame.chat_id }, { kind: 'session', chatId: frame.chat_id })
  }
  if (frame.type === 'error') {
    return append(state, { kind: 'text', role: 'system', raw: `Error: ${frame.message || 'unknown'}`, final: true })
  }
  if (frame.type === 'thinking_start' || frame.type === 'thinking_end') {
    return { ...state, thinking: frame.type === 'thinking_start' }
  }
  if (frame.type === 'message') {
    return append(state, {
      kind: 'text',
      role: 'assistant',
      raw: frame.text || '',
      final: true,
      metadata: frame.metadata
    })
  }
  if (frame.type === 'message_stream_delta' || frame.type === 'message_delta') {
    const key = streamKey(frame)
    const delta = frame.type === 'message_stream_delta' ? frame.text || '' : frame.delta || ''
    if (!delta) return state
    const index = state.timeline.findIndex(
      (item) => item.kind === 'text' && item.role === 'assistant' && !item.final && item.streamKey === key
    )
    if (index < 0) {
      return append(state, { kind: 'text', role: 'assistant', raw: delta, final: false, streamKey: key })
    }
    const timeline = state.timeline.slice()
    const item = timeline[index] as TextItem
    timeline[index] = { ...item, raw: item.raw + delta }
    return { ...state, timeline }
  }
  if (frame.type === 'message_stream_end' || frame.type === 'message_end') {
    const key = streamKey(frame)
    const timeline = state.timeline
      .map((item) =>
        item.kind === 'text' && !item.final && item.streamKey === key
          ? { ...item, final: true, metadata: frame.metadata }
          : item
      )
      .filter((item) => item.kind !== 'text' || !item.final || item.raw.trim())
    return { ...state, timeline }
  }
  if (frame.type === 'tool_call') {
    return append(state, {
      kind: 'tool',
      toolCallId: frame.tool_call_id,
      name: frame.name || 'tool',
      args: frame.args || {},
      reasoning: frame.reasoning,
      status: 'running'
    })
  }
  if (frame.type === 'tool_result') {
    return {
      ...state,
      timeline: state.timeline.map((item) =>
        item.kind === 'tool' && item.toolCallId === frame.tool_call_id
          ? { ...item, status: frame.ok ? 'done' : 'error', preview: frame.preview }
          : item
      )
    }
  }
  if (frame.type === 'image') {
    return append(state, {
      kind: 'image',
      role: 'assistant',
      src: `data:${frame.mime_type || 'image/png'};base64,${frame.data}`,
      caption: ''
    })
  }
  return state
}

export function chatReducer(state: ChatState, action: ChatAction): ChatState {
  if (action.type === 'connecting') return { ...state, status: 'connecting' }
  if (action.type === 'connection_failed') {
    return append(
      { ...state, status: 'disconnected' },
      {
        kind: 'text',
        role: 'system',
        raw: action.message,
        final: true
      }
    )
  }
  if (action.type === 'socket_error') {
    return append(state, { kind: 'text', role: 'system', raw: 'WebSocket error.', final: true })
  }
  if (action.type === 'closed') {
    return append(
      { ...state, status: 'disconnected', chatId: null, thinking: false, timeline: finishStreamsAndTools(state) },
      { kind: 'text', role: 'system', raw: 'Disconnected.', final: true }
    )
  }
  if (action.type === 'sent_text') {
    return append(state, { kind: 'text', role: 'user', raw: action.text, final: true })
  }
  if (action.type === 'sent_image') {
    return append(state, { kind: 'image', role: 'user', src: action.src, caption: action.caption })
  }
  return reduceFrame(state, action.frame)
}
