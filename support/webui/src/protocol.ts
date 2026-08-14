export type ResponseMetadata = {
  model?: string
  completion_tokens?: number
  generation_seconds?: number
  total_tokens?: number
  context_window?: number
}

export type ConnectedFrame = { type: 'connected'; chat_id: string }
export type ErrorFrame = { type: 'error'; message?: string }
export type ThinkingFrame = { type: 'thinking_start' | 'thinking_end' }
export type MessageFrame = { type: 'message'; text?: string; message_id?: string; metadata?: ResponseMetadata }
export type StreamDeltaFrame = {
  type: 'message_stream_delta'
  text?: string
  message_id?: string
  stream_id?: string
  chat_id?: string
}
export type StreamEndFrame = {
  type: 'message_stream_end'
  message_id?: string
  stream_id?: string
  chat_id?: string
  metadata?: ResponseMetadata
}
export type LegacyDeltaFrame = {
  type: 'message_delta'
  delta?: string
  message_id?: string
  stream_id?: string
  chat_id?: string
}
export type LegacyEndFrame = {
  type: 'message_end'
  message_id?: string
  stream_id?: string
  chat_id?: string
  metadata?: ResponseMetadata
}
export type ToolCallFrame = {
  type: 'tool_call'
  tool_call_id: string
  name?: string
  args?: Record<string, unknown>
  reasoning?: string
}
export type ToolResultFrame = {
  type: 'tool_result'
  tool_call_id: string
  ok?: boolean
  preview?: string
}
export type ImageFrame = { type: 'image'; data: string; mime_type?: string }

export type InboundFrame =
  | ConnectedFrame
  | ErrorFrame
  | ThinkingFrame
  | MessageFrame
  | StreamDeltaFrame
  | StreamEndFrame
  | LegacyDeltaFrame
  | LegacyEndFrame
  | ToolCallFrame
  | ToolResultFrame
  | ImageFrame

export type OutboundFrame =
  | { type: 'text'; message: string; message_id: string }
  | { type: 'image'; message_id: string; data: string; mime_type: string; message: string }

const frameTypes = new Set([
  'connected',
  'error',
  'thinking_start',
  'thinking_end',
  'message',
  'message_stream_delta',
  'message_stream_end',
  'message_delta',
  'message_end',
  'tool_call',
  'tool_result',
  'image'
])

export function parseInboundFrame(value: string): InboundFrame | null {
  try {
    const parsed: unknown = JSON.parse(value)
    if (!parsed || typeof parsed !== 'object' || !('type' in parsed)) return null
    const type = (parsed as { type?: unknown }).type
    return typeof type === 'string' && frameTypes.has(type) ? (parsed as InboundFrame) : null
  } catch {
    return null
  }
}

export function streamKey(frame: StreamDeltaFrame | StreamEndFrame | LegacyDeltaFrame | LegacyEndFrame) {
  return frame.message_id || frame.stream_id || frame.chat_id || '__default_stream__'
}

export function defaultEndpoint(location: Pick<Location, 'protocol' | 'host' | 'search'> = window.location) {
  const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
  const token = new URLSearchParams(location.search).get('token')
  return `${protocol}//${location.host}/api/bot${token ? `?token=${encodeURIComponent(token)}` : ''}`
}
