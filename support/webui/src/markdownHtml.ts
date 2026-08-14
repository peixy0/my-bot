import DOMPurify from 'dompurify'
import { Marked } from 'marked'

export function markdownHtml(raw: string, final: boolean) {
  const parser = new Marked()
  parser.use({
    renderer: {
      code(token) {
        if (final && token.lang === 'mermaid') {
          return `<div class="mermaid">${DOMPurify.sanitize(token.text)}</div>`
        }
        return false
      }
    }
  })
  const parsed = parser.parse(raw, { gfm: true, breaks: false })
  return DOMPurify.sanitize(typeof parsed === 'string' ? parsed : '', {
    USE_PROFILES: { html: true }
  })
}
