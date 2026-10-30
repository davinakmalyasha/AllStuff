"use client"

import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import en from '../i18n/en.json'
import id from '../i18n/id.json'

/**
 * Preferred-language resolution (saved pref, else navigator). Deliberately
 * NOT used as the init `lng` below: the server always prerenders English, so
 * initializing the client with anything else makes the first client render
 * disagree with the SSR HTML — a hydration mismatch for every returning
 * Indonesian-language user. Instead the app boots English (matching the
 * server byte-for-byte) and Providers switches to the preferred language in
 * an effect after mount: a normal update, no regeneration, no console error.
 * A full server-locale render (e.g. next-intl) would remove even the
 * first-paint swap; until then this is the honest tradeoff, stated here.
 */
export function resolvePreferredLng(): string {
  try {
    const saved = localStorage.getItem('bv.lang')
    if (saved === 'en' || saved === 'id') return saved
    const nav = (navigator.language || 'en').toLowerCase()
    if (nav.startsWith('id')) return 'id'
  } catch {
    /* noop */
  }
  return 'en'
}

i18n.use(initReactI18next).init({
  resources: {
    en: { translation: en },
    id: { translation: id },
  },
  lng: 'en',
  fallbackLng: 'en',
  interpolation: { escapeValue: false },
})

// Keep <html lang> in sync so screen readers and search engines see the
// active language (en/id).
function syncDocLang(lng: string) {
  try {
    document.documentElement.lang = lng?.startsWith('id') ? 'id' : 'en'
  } catch {
    /* noop */
  }
}
syncDocLang(i18n.language)
i18n.on('languageChanged', syncDocLang)

export function setLanguage(lng: 'en' | 'id') {
  try {
    localStorage.setItem('bv.lang', lng)
  } catch {
    /* noop */
  }
  void i18n.changeLanguage(lng)
}

export default i18n
