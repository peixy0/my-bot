import { useEffect, useMemo, useRef } from 'react'
import { markdownHtml } from './markdownHtml'

type Mermaid = (typeof import('mermaid'))['default']
let mermaidLoader: Promise<Mermaid> | null = null

function loadMermaid() {
  if (!mermaidLoader) {
    mermaidLoader = import('mermaid').then(({ default: mermaid }) => {
      mermaid.initialize({ startOnLoad: false, theme: 'default', securityLevel: 'strict' })
      return mermaid
    })
  }
  return mermaidLoader
}

export function Markdown({
  raw,
  final,
  onOpenImage
}: {
  raw: string
  final: boolean
  onOpenImage: (src: string) => void
}) {
  const rootRef = useRef<HTMLDivElement>(null)
  const html = useMemo(() => markdownHtml(raw, final), [raw, final])

  useEffect(() => {
    const root = rootRef.current
    if (!root || !final) return
    const diagrams = Array.from(root.querySelectorAll<HTMLElement>('.mermaid'))
    if (!diagrams.length) return
    let active = true
    loadMermaid()
      .then((mermaid) => mermaid.run({ nodes: diagrams, suppressErrors: true }))
      .then(() => {
        if (!active) return
        diagrams.forEach((diagram) => {
          diagram.tabIndex = 0
          diagram.setAttribute('role', 'button')
          diagram.setAttribute('aria-label', 'Open diagram')
          const open = () => {
            const svg = diagram.querySelector('svg')
            if (!svg) return
            const source = new XMLSerializer().serializeToString(svg)
            onOpenImage(`data:image/svg+xml;charset=utf-8,${encodeURIComponent(source)}`)
          }
          diagram.onclick = open
          diagram.onkeydown = (event) => {
            if (event.key === 'Enter' || event.key === ' ') open()
          }
        })
      })
      .catch(() => undefined)
    return () => {
      active = false
    }
  }, [final, html, onOpenImage])

  return <div ref={rootRef} className="markdown" dangerouslySetInnerHTML={{ __html: html }} />
}
