// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors
// How a game's page is coloured by its cover: copies of the app's own (web/src/pages/games/data.ts
// and Library.tsx), which a module can't import.
import { useEffect, useRef, useState } from 'react'
import { posterUrl, type Item } from '@wms/app/api'

const dense = (window.devicePixelRatio || 1) * window.innerWidth > 2000
export const heroCover = (it: Item) => `${posterUrl(it)}&w=${dense ? 800 : 480}`

// A cover's main colour, from a very small copy of it. It costs one small picture and
// no blur, so a cheap TV can afford a page tinted with it.
const tints = new Map<string, [number, number, number] | null>()

function averageColour(img: HTMLImageElement): [number, number, number] | null {
  const size = 16
  const c = document.createElement('canvas')
  c.width = c.height = size
  const g = c.getContext('2d', { willReadFrequently: true })
  if (!g) return null
  g.drawImage(img, 0, 0, size, size)
  let data: Uint8ClampedArray
  try { data = g.getImageData(0, 0, size, size).data } catch { return null } // a picture from another address
  let r = 0, gr = 0, b = 0, w = 0
  for (let i = 0; i < data.length; i += 4) {
    const R = data[i], G = data[i + 1], B = data[i + 2]
    const max = Math.max(R, G, B), min = Math.min(R, G, B)
    // The colourful parts count for more: a white box with a red logo reads as red.
    const weight = 1 + ((max - min) / 255) * 6
    r += R * weight; gr += G * weight; b += B * weight; w += weight
  }
  return w ? [r / w, gr / w, b / w] : null
}

function hsl([r, g, b]: [number, number, number]): [number, number, number] {
  r /= 255; g /= 255; b /= 255
  const max = Math.max(r, g, b), min = Math.min(r, g, b), l = (max + min) / 2, d = max - min
  if (!d) return [0, 0, l]
  const s = d / (1 - Math.abs(2 * l - 1))
  const h = max === r ? ((g - b) / d + 6) % 6 : max === g ? (b - r) / d + 2 : (r - g) / d + 4
  return [h * 60, s, l]
}

// useCoverTint gives a colour for the page behind a cover: deep on a dark theme, pale on
// a light one, so that text stays readable on it either way.
export function useCoverTint(url: string | null): string | null {
  const [tint, setTint] = useState<[number, number, number] | null>(null)
  useEffect(() => {
    if (!url) { setTint(null); return }
    if (tints.has(url)) { setTint(tints.get(url)!); return }
    let stale = false
    const img = new Image()
    img.decoding = 'async'
    img.onload = () => { const t = averageColour(img); tints.set(url, t); if (!stale) setTint(t) }
    img.onerror = () => { tints.set(url, null); if (!stale) setTint(null) }
    img.src = url
    return () => { stale = true }
  }, [url])
  if (!tint) return null
  const [h, s] = hsl(tint)
  return isDarkBackground()
    ? `hsl(${Math.round(h)} ${Math.round(Math.min(s, 0.62) * 100)}% 26%)`
    : `hsl(${Math.round(h)} ${Math.round(Math.min(s, 0.7) * 100)}% 84%)`
}

function isDarkBackground() {
  const v = getComputedStyle(document.documentElement).getPropertyValue('--bg-rgb').trim().split(/\s+/).map(Number)
  if (v.length < 3 || v.some(Number.isNaN)) return true
  return 0.2126 * v[0] + 0.7152 * v[1] + 0.0722 * v[2] < 128
}

// The page behind a cover, tinted with it, fading from one colour to the next.
export function Ambient({ colour }: { colour: string | null }) {
  const layers = useRef<(HTMLDivElement | null)[]>([])
  const front = useRef(0)
  useEffect(() => {
    if (!colour) return
    const next = 1 - front.current
    const a = layers.current[next], b = layers.current[front.current]
    if (!a || !b) return
    a.style.setProperty('--gm-tint', colour)
    a.style.opacity = '1'
    b.style.opacity = '0'
    front.current = next
  }, [colour])
  return (
    <div className="detail-hero gm-ambient" aria-hidden="true">
      <div ref={(el) => { layers.current[0] = el }} className="gm-ambient-layer" />
      <div ref={(el) => { layers.current[1] = el }} className="gm-ambient-layer" />
    </div>
  )
}
