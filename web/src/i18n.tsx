import { createContext, useContext, useEffect, useState, type ReactNode } from 'react';

export type Lang = 'en' | 'tr';

const LANG_KEY = 'gpuhub_lang';

interface LangCtx {
  lang: Lang;
  setLang: (l: Lang) => void;
}

const Ctx = createContext<LangCtx>({ lang: 'en', setLang: () => {} });

export function getInitialLang(): Lang {
  try {
    const saved = localStorage.getItem(LANG_KEY);
    if (saved === 'tr' || saved === 'en') return saved;
  } catch {
    /* ignore */
  }
  // Browser locale: Turkish users get Turkish by default.
  try {
    if ((navigator.language || '').toLowerCase().startsWith('tr')) return 'tr';
  } catch {
    /* ignore */
  }
  return 'en';
}

export function LangProvider({ children }: { children: ReactNode }) {
  const [lang, setLangState] = useState<Lang>(() => getInitialLang());
  const setLang = (l: Lang) => {
    setLangState(l);
    try {
      localStorage.setItem(LANG_KEY, l);
    } catch {
      /* ignore */
    }
    document.documentElement.lang = l;
  };
  useEffect(() => {
    document.documentElement.lang = lang;
  }, [lang]);
  return <Ctx.Provider value={{ lang, setLang }}>{children}</Ctx.Provider>;
}

export function useLang(): Lang {
  return useContext(Ctx).lang;
}

export function useSetLang() {
  return useContext(Ctx).setLang;
}

/** Bilingual inline string: L(lang, {en: 'Save', tr: 'Kaydet'}) */
export function L(lang: Lang, dict: { en: string; tr: string }): string {
  return dict[lang] ?? dict.en;
}
