import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'

// This module reads the stored preference and applies it at import time, so
// every case needs a fresh copy of it — otherwise the first import would
// decide the language for all the others.
async function loadI18n() {
  vi.resetModules()
  return await import('./index')
}

function setNavigatorLanguage(value: string) {
  Object.defineProperty(window.navigator, 'language', { value, configurable: true })
}

beforeEach(() => {
  localStorage.clear()
  // index.html ships lang="zh-CN"; every case starts from that.
  document.documentElement.lang = 'zh-CN'
  setNavigatorLanguage('en-US')
})

afterEach(() => {
  Reflect.deleteProperty(window.navigator, 'language')
})

describe('start-up', () => {
  it('applies the stored English preference', async () => {
    localStorage.setItem('repotory-language', 'en')
    await loadI18n()
    expect(document.documentElement.lang).toBe('en')
  })

  it('applies the stored Chinese preference', async () => {
    localStorage.setItem('repotory-language', 'zh-CN')
    await loadI18n()
    expect(document.documentElement.lang).toBe('zh-CN')
  })

  it('follows the browser language when nothing is stored', async () => {
    setNavigatorLanguage('zh-CN')
    await loadI18n()
    expect(document.documentElement.lang).toBe('zh-CN')
  })

  it('falls back to English for a browser language Cinder does not ship', async () => {
    setNavigatorLanguage('de-DE')
    await loadI18n()
    expect(document.documentElement.lang).toBe('en')
  })

  it('ignores a stored value Cinder does not know', async () => {
    setNavigatorLanguage('zh-CN')
    localStorage.setItem('repotory-language', 'klingon')
    await loadI18n()
    expect(document.documentElement.lang).toBe('zh-CN')
  })
})

describe('setLanguage', () => {
  it('switches the attribute and stores the preference', async () => {
    const i18n = await loadI18n()
    i18n.setLanguage('en')
    expect(document.documentElement.lang).toBe('en')
    expect(localStorage.getItem('repotory-language')).toBe('en')
  })

  it('switches back to Chinese', async () => {
    const i18n = await loadI18n()
    i18n.setLanguage('en')
    i18n.setLanguage('zh-CN')
    expect(document.documentElement.lang).toBe('zh-CN')
    expect(localStorage.getItem('repotory-language')).toBe('zh-CN')
  })
})
