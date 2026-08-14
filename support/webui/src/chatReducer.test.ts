import { chatReducer, initialChatState } from './chatReducer'

describe('chatReducer', () => {
  it('groups stream deltas and finalizes metadata', () => {
    let state = chatReducer(initialChatState, {
      type: 'frame',
      frame: { type: 'message_stream_delta', message_id: 'm1', text: 'Hello' }
    })
    state = chatReducer(state, {
      type: 'frame',
      frame: { type: 'message_delta', message_id: 'm1', delta: ' world' }
    })
    state = chatReducer(state, {
      type: 'frame',
      frame: { type: 'message_stream_end', message_id: 'm1', metadata: { model: 'test-model' } }
    })

    expect(state.timeline).toHaveLength(1)
    expect(state.timeline[0]).toMatchObject({
      kind: 'text',
      raw: 'Hello world',
      final: true,
      metadata: { model: 'test-model' }
    })
  })

  it('correlates tools and cancels running tools on close', () => {
    let state = chatReducer(initialChatState, {
      type: 'frame',
      frame: { type: 'tool_call', tool_call_id: 'done', name: 'read_file', args: { path: 'a' } }
    })
    state = chatReducer(state, {
      type: 'frame',
      frame: { type: 'tool_result', tool_call_id: 'done', ok: true, preview: 'ok' }
    })
    state = chatReducer(state, {
      type: 'frame',
      frame: { type: 'tool_call', tool_call_id: 'running', name: 'grep' }
    })
    state = chatReducer(state, { type: 'closed' })

    expect(state.timeline[0]).toMatchObject({ kind: 'tool', status: 'done', preview: 'ok' })
    expect(state.timeline[1]).toMatchObject({ kind: 'tool', status: 'cancelled', preview: 'cancelled' })
    expect(state.timeline[2]).toMatchObject({ kind: 'text', raw: 'Disconnected.' })
  })

  it('preserves the timeline and adds a divider for each session', () => {
    let state = chatReducer(initialChatState, {
      type: 'frame',
      frame: { type: 'connected', chat_id: 'first' }
    })
    state = chatReducer(state, { type: 'sent_text', text: 'keep me' })
    state = chatReducer(state, { type: 'closed' })
    state = chatReducer(state, {
      type: 'frame',
      frame: { type: 'connected', chat_id: 'second' }
    })

    expect(state.timeline.filter((item) => item.kind === 'session')).toHaveLength(2)
    expect(state.timeline.some((item) => item.kind === 'text' && item.raw === 'keep me')).toBe(true)
    expect(state.chatId).toBe('second')
  })
})
