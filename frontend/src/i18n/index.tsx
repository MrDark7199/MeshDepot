import { createContext, useContext, createSignal, JSX } from 'solid-js'
import en from './en'
import de from './de'

type TranslationKeys = typeof en
const AVAILABLE_LANGUAGES: Record<string, TranslationKeys> = { en, de }
export const availableLangs = ['en', 'de']

const LANGUAGE_STORAGE_KEY = 'meshdepot_lang'
const TRANSLATE_DESIGNS_KEY = 'meshdepot_translate_designs'

interface I18nContextType {
  translate: (key: string, vars?: Record<string, string | number>) => string
  /** Short alias of {@link translate}. Provided so consumers never hand-roll a
   *  `const t = translate` alias (a source of copy-paste bugs). */
  t: (key: string, vars?: Record<string, string | number>) => string
  lang: () => string
  /** Changes the active language and persists the selection. */
  setLang: (languageCode: string) => void
  availableLangs: string[]
  /** Whether designs should be shown translated into the display language (vs. the original). */
  translateDesigns: () => boolean
  /** Changes the design-translation display preference and persists it. */
  setTranslateDesigns: (enabled: boolean) => void
}

const I18nContext = createContext<I18nContextType>({} as I18nContextType)

/**
 * Provides internationalization support throughout the application.
 * Language selection is persisted in localStorage.
 */
export function I18nProvider(props: { children: JSX.Element }) {
  // English unless this browser has been switched deliberately. The browser's
  // own language used to decide, which made a fresh account start in German
  // while the server had it stored as English - two answers to one question.
  const storedLanguage = localStorage.getItem(LANGUAGE_STORAGE_KEY)
  const defaultLanguage = storedLanguage && availableLangs.includes(storedLanguage) ? storedLanguage : 'en'

  const [activeLanguage, setActiveLanguage] = createSignal(defaultLanguage)

  const setLang = (languageCode: string) => {
    localStorage.setItem(LANGUAGE_STORAGE_KEY, languageCode)
    setActiveLanguage(languageCode)
  }

  // Design-translation display preference (default on); persisted like the language.
  const [translateDesigns, setTranslateDesignsSignal] = createSignal(localStorage.getItem(TRANSLATE_DESIGNS_KEY) !== '0')
  const setTranslateDesigns = (enabled: boolean) => {
    localStorage.setItem(TRANSLATE_DESIGNS_KEY, enabled ? '1' : '0')
    setTranslateDesignsSignal(enabled)
  }

  /**
   * Translates a key to the current language.
   * Falls back to English if the key is not found in the active language.
   * Falls back to the raw key if not found in English either.
   */
  const translate = (key: string, vars?: Record<string, string | number>): string => {
    const dictionary = AVAILABLE_LANGUAGES[activeLanguage()] || en
    let translatedString: string = (dictionary as any)[key] ?? (en as any)[key] ?? key
    if (vars) {
      Object.entries(vars).forEach(([variableName, value]) => {
        translatedString = translatedString.replace(`{${variableName}}`, String(value))
      })
    }
    return translatedString
  }

  return (
    <I18nContext.Provider value={{ translate, t: translate, lang: activeLanguage, setLang, availableLangs, translateDesigns, setTranslateDesigns }}>
      {props.children}
    </I18nContext.Provider>
  )
}

/** Hook to access the i18n context. Must be used inside an {@link I18nProvider}. */
export const useI18n = () => useContext(I18nContext)
