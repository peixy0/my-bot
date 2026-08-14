import { useEffect, useRef, useState, type PointerEvent, type WheelEvent } from 'react'

type View = { scale: number; x: number; y: number }

export function Lightbox({ src, onClose }: { src: string | null; onClose: () => void }) {
  if (!src) return null
  return <LightboxView key={src} src={src} onClose={onClose} />
}

function LightboxView({ src, onClose }: { src: string; onClose: () => void }) {
  const [view, setView] = useState<View>({ scale: 1, x: 0, y: 0 })
  const [dragging, setDragging] = useState(false)
  const dragRef = useRef<{ mouseX: number; mouseY: number; x: number; y: number; moved: boolean } | null>(null)
  const frameRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [onClose])

  const fit = (image: HTMLImageElement) => {
    const frame = frameRef.current
    if (!frame || !image.naturalWidth || !image.naturalHeight) return
    const scale = Math.min(
      1,
      (frame.clientWidth - 40) / image.naturalWidth,
      (frame.clientHeight - 40) / image.naturalHeight
    )
    setView({
      scale,
      x: (frame.clientWidth - image.naturalWidth * scale) / 2,
      y: (frame.clientHeight - image.naturalHeight * scale) / 2
    })
  }

  const zoom = (event: WheelEvent) => {
    event.preventDefault()
    const nextScale = Math.min(20, Math.max(0.1, view.scale * (event.deltaY < 0 ? 1.15 : 1 / 1.15)))
    setView({
      scale: nextScale,
      x: event.clientX - (event.clientX - view.x) * (nextScale / view.scale),
      y: event.clientY - (event.clientY - view.y) * (nextScale / view.scale)
    })
  }

  const startDrag = (event: PointerEvent) => {
    if (event.button !== 0) return
    dragRef.current = { mouseX: event.clientX, mouseY: event.clientY, x: view.x, y: view.y, moved: false }
    setDragging(true)
  }

  const drag = (event: PointerEvent) => {
    const origin = dragRef.current
    if (!origin) return
    const dx = event.clientX - origin.mouseX
    const dy = event.clientY - origin.mouseY
    if (Math.abs(dx) > 3 || Math.abs(dy) > 3) origin.moved = true
    setView((current) => ({ ...current, x: origin.x + dx, y: origin.y + dy }))
  }

  const stopDrag = () => {
    const moved = dragRef.current?.moved
    dragRef.current = null
    setDragging(false)
    if (!moved) onClose()
  }

  return (
    <div
      ref={frameRef}
      className={`lightbox${dragging ? ' dragging' : ''}`}
      role="dialog"
      aria-modal="true"
      aria-label="Image viewer"
      tabIndex={-1}
      onWheel={zoom}
      onPointerDown={startDrag}
      onPointerMove={drag}
      onPointerUp={stopDrag}
      onPointerLeave={() => {
        dragRef.current = null
        setDragging(false)
      }}
    >
      <button className="lightbox-close" type="button" aria-label="Close image viewer" onClick={onClose}>
        ×
      </button>
      <img
        src={src}
        alt="Full size"
        draggable={false}
        onLoad={(event) => fit(event.currentTarget)}
        style={{ transform: `translate(${view.x}px, ${view.y}px) scale(${view.scale})` }}
      />
    </div>
  )
}
