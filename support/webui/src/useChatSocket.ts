import { useCallback, useEffect, useReducer, useRef } from 'react'
import { chatReducer, initialChatState } from './chatReducer'
import { parseInboundFrame, streamKey, type OutboundFrame, type StreamDeltaFrame } from './protocol'

export function useChatSocket(initialEndpoint: string) {
  const [state, dispatch] = useReducer(chatReducer, initialChatState)
  const socketRef = useRef<WebSocket | null>(null)
  const sequenceRef = useRef(0)
  const pendingRef = useRef(new Map<string, { frame: StreamDeltaFrame; timer: number }>())

  const flushDelta = useCallback((key: string) => {
    const pending = pendingRef.current.get(key)
    if (!pending) return
    window.clearTimeout(pending.timer)
    pendingRef.current.delete(key)
    dispatch({ type: 'frame', frame: pending.frame })
  }, [])

  const connect = useCallback(
    (endpoint: string) => {
      const current = socketRef.current
      if (current && (current.readyState === WebSocket.OPEN || current.readyState === WebSocket.CONNECTING)) return
      dispatch({ type: 'connecting' })
      let socket: WebSocket
      try {
        socket = new WebSocket(endpoint)
      } catch (error) {
        const message = error instanceof Error ? error.message : String(error)
        dispatch({ type: 'connection_failed', message: `Invalid URL: ${message}` })
        return
      }
      socketRef.current = socket
      socket.onmessage = (event) => {
        if (typeof event.data !== 'string') return
        const frame = parseInboundFrame(event.data)
        if (!frame) return
        if (frame.type === 'message_stream_delta' || frame.type === 'message_delta') {
          const key = streamKey(frame)
          const delta = frame.type === 'message_stream_delta' ? frame.text || '' : frame.delta || ''
          if (!delta) return
          const current = pendingRef.current.get(key)
          if (current) {
            current.frame.text = `${current.frame.text || ''}${delta}`
            return
          }
          const normalized: StreamDeltaFrame = {
            type: 'message_stream_delta',
            text: delta,
            message_id: frame.message_id,
            stream_id: frame.stream_id,
            chat_id: frame.chat_id
          }
          const timer = window.setTimeout(() => flushDelta(key), 80)
          pendingRef.current.set(key, { frame: normalized, timer })
          return
        }
        if (frame.type === 'message_stream_end' || frame.type === 'message_end') flushDelta(streamKey(frame))
        dispatch({ type: 'frame', frame })
      }
      socket.onerror = () => dispatch({ type: 'socket_error' })
      socket.onclose = () => {
        if (socketRef.current === socket) socketRef.current = null
        Array.from(pendingRef.current.keys()).forEach(flushDelta)
        dispatch({ type: 'closed' })
      }
    },
    [flushDelta]
  )

  const disconnect = useCallback(() => {
    socketRef.current?.close()
  }, [])

  const send = useCallback((frame: OutboundFrame) => {
    const socket = socketRef.current
    if (!socket || socket.readyState !== WebSocket.OPEN) return false
    socket.send(JSON.stringify(frame))
    return true
  }, [])

  const nextMessageId = useCallback(() => {
    sequenceRef.current += 1
    return `ui-${Date.now()}-${sequenceRef.current}`
  }, [])

  useEffect(() => {
    const pending = pendingRef.current
    connect(initialEndpoint)
    return () => {
      const socket = socketRef.current
      socketRef.current = null
      pending.forEach((entry) => window.clearTimeout(entry.timer))
      pending.clear()
      if (socket) {
        socket.onmessage = null
        socket.onerror = null
        socket.onclose = null
        socket.close()
      }
    }
  }, [connect, initialEndpoint])

  return { state, dispatch, connect, disconnect, send, nextMessageId }
}
