import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import en from '../i18n/en.json'
import id from '../i18n/id.json'

function detectLng(): string {
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
  lng: detectLng(),
  fallbackLng: 'en',
  interpolation: { escapeValue: false },
})

export function setLanguage(lng: 'en' | 'id') {
  try {
    localStorage.setItem('bv.lang', lng)
  } catch {
    /* noop */
  }
  void i18n.changeLanguage(lng)
}

export default i18n
