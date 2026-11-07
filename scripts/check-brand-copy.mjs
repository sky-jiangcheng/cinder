#!/usr/bin/env node
/**
 * Brand copy gate — ADR-0052 decision 7.
 *
 * build/brand-copy.json is the single source of truth for the 25 zh/en
 * baselines. Like the masters, copy must not drift: this checks the
 * baseline's shape, keeps the banned-word list out of both the baselines
 * and the four shop-window pages, pins the positioning lines that are
 * literally shipped, and ties the `marks` block back to the two master
 * SVGs so the copy cannot describe a mark we no longer ship.
 *
 * Zero dependencies, same shape as check-docs-mirror.mjs.
 */
import fs from 'node:fs'
import path from 'node:path'

const root = process.cwd()
const read = (rel) => fs.readFileSync(path.join(root, rel), 'utf8')
const issues = []
const copy = JSON.parse(read('build/brand-copy.json'))

// 1. Baseline shape. 25 surfaces, each with both columns.
if (copy.version !== '6') issues.push(`version "${copy.version}" != 6`)
if (!Array.isArray(copy.surfaces) || copy.surfaces.length !== 25) {
  issues.push(`surfaces: expected 25, got ${copy.surfaces?.length ?? 'missing'}`)
}
for (const s of copy.surfaces ?? []) {
  if (!s.id || !s.surface || !s.zh || !s.en) issues.push(`surface ${s.id}: id/surface/zh/en 必须齐全`)
}

// 2. Banned words: neither in the baselines nor on the pages users see first.
const FACES = ['README.md', 'README.zh-CN.md', 'docs/index.md', 'docs/en/index.md']
const hasEn = (text, w) => new RegExp(`\\b${w}\\b`, 'i').test(text)
for (const s of copy.surfaces ?? []) {
  for (const w of copy.banned.zh) {
    if (s.zh.includes(w)) issues.push(`baseline ${s.id} (zh) 含禁用词「${w}」`)
  }
  for (const w of copy.banned.en) {
    if (hasEn(s.en, w)) issues.push(`baseline ${s.id} (en) contains banned "${w}"`)
  }
}
for (const f of FACES) {
  const text = read(f)
  for (const w of copy.banned.zh) if (text.includes(w)) issues.push(`${f}: 禁用词「${w}」`)
  for (const w of copy.banned.en) if (hasEn(text, w)) issues.push(`${f}: banned word "${w}"`)
}

// 3. The positioning lines are shipped verbatim (docs omit the final period).
const trim = (s) => s.replace(/[.。]$/, '')
const POSITIONING = [
  ['README.md', copy.positioning.cinder.en],
  ['README.zh-CN.md', copy.positioning.cinder.zh],
  ['docs/index.md', trim(copy.positioning.cinder.en)],
  ['docs/index.md', trim(copy.positioning.cinder.zh)],
  ['docs/en/index.md', trim(copy.positioning.cinder.en)],
]
for (const [f, line] of POSITIONING) {
  if (!read(f).includes(line)) issues.push(`${f}: 定位句不在页面上 —「${line}」`)
}

// 4. The copy's `marks` block describes the masters we actually ship.
const cinder = read('build/icon.svg')
const warden = read('build/icon-warden.svg')
const pathCount = (s) => [...s.matchAll(/<path[^>]*\sd="([^"]+)"/g)].length
const field = (s) => /<rect width="512" height="512" fill="([^"]+)"/.exec(s)?.[1]
const glyph = (s) => /<path[^>]*\sfill="([^"]+)"/.exec(s)?.[1]
if (pathCount(cinder) !== copy.marks.cinder.paths) {
  issues.push(`marks.cinder.paths ${copy.marks.cinder.paths} != 母图 ${pathCount(cinder)}`)
}
if (pathCount(warden) !== copy.marks.warden.paths) {
  issues.push(`marks.warden.paths ${copy.marks.warden.paths} != Warden 母图 ${pathCount(warden)}`)
}
if (field(cinder) !== copy.marks.colors.field || glyph(cinder) !== copy.marks.colors.glyph) {
  issues.push(`marks.colors 与母图 ${field(cinder)} / ${glyph(cinder)} 不一致`)
}
if (field(warden) !== copy.marks.colors.field || glyph(warden) !== copy.marks.colors.glyph) {
  issues.push(`Warden 母图配色与基线不一致`)
}
if (copy.marks.cinder.bandPx !== copy.marks.warden.bandPx) {
  issues.push('家族规则「等宽带」被破坏：两枚 bandPx 不相等')
}

if (issues.length) {
  console.error(`✗ brand copy drift (${issues.length}):`)
  for (const i of issues) console.error(`  - ${i}`)
  process.exit(1)
}
console.log(`brand copy OK: ${copy.surfaces.length} 条中英基线、${copy.banned.zh.length + copy.banned.en.length} 个禁用词、4 份门面页与两张母图同源`)
