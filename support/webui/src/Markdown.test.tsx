import { markdownHtml } from './markdownHtml'

vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    run: vi.fn().mockResolvedValue(undefined)
  }
}))

describe('markdownHtml', () => {
  it('sanitizes scripts, event handlers, and unsafe links', () => {
    const html = markdownHtml(
      '<script>alert(1)</script><img src="x" onerror="alert(2)">[bad](javascript:alert(3))',
      true
    )

    expect(html).not.toContain('<script')
    expect(html).not.toContain('onerror')
    expect(html).not.toContain('href=')
    expect(html).toContain('<img src="x">')
  })

  it('only exposes Mermaid diagrams when final', () => {
    const source = '```mermaid\ngraph TD\nA-->B\n```'

    expect(markdownHtml(source, false)).toContain('<pre><code')
    expect(markdownHtml(source, false)).not.toContain('class="mermaid"')
    expect(markdownHtml(source, true)).toContain('class="mermaid"')
  })
})
