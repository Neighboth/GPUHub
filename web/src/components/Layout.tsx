import { useEffect, useRef, useState } from 'react';
import { NavLink, Outlet, useNavigate, useLocation } from 'react-router-dom';
import {
  LayoutDashboard,
  Server,
  Network,
  KeyRound,
  Settings,
  ScrollText,
  LogOut,
  ChevronDown,
  UserRound,
  Menu,
  X,
  ShieldCheck,
  Sun,
  Moon,
  Globe,
  Sparkles,
} from 'lucide-react';
import { api, getStoredUser, type StoredUser } from '../api';
import Logo from './Logo';
import { useLang, useSetLang, L, type Lang } from '../i18n';

type NavDef = { to: string; en: string; tr: string; icon: typeof Server; end: boolean };

const NAV: NavDef[] = [
  { to: '/', en: 'Dashboard', tr: 'Panel', icon: LayoutDashboard, end: true },
  { to: '/ai-test', en: 'AI Test', tr: 'AI Testi', icon: Sparkles, end: false },
  { to: '/providers', en: 'Providers', tr: 'Sağlayıcılar', icon: Server, end: false },
  { to: '/proxies', en: 'Proxies', tr: 'Proxyler', icon: Network, end: false },
  { to: '/keys', en: 'Keys', tr: 'Anahtarlar', icon: KeyRound, end: false },
  { to: '/settings', en: 'Settings', tr: 'Ayarlar', icon: Settings, end: false },
  { to: '/audit', en: 'Audit Log', tr: 'Denetim Kaydı', icon: ScrollText, end: false },
];

function initials(name?: string): string {
  if (!name) return 'U';
  const parts = name.trim().split(/[\s._-]+/).filter(Boolean);
  if (parts.length === 0) return name.slice(0, 1).toUpperCase();
  return parts.slice(0, 2).map((p) => p[0]?.toUpperCase() ?? '').join('');
}

const THEME_KEY = 'gpuhub_theme';

export default function Layout() {
  const navigate = useNavigate();
  const location = useLocation();
  const lang = useLang();
  const setLang = useSetLang();
  const [user, setUser] = useState<StoredUser | null>(() => getStoredUser());
  const [menuOpen, setMenuOpen] = useState(false);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [langOpen, setLangOpen] = useState(false);
  const [theme, setTheme] = useState<'dark' | 'light'>(() => {
    try {
      return (localStorage.getItem(THEME_KEY) as 'dark' | 'light') || 'dark';
    } catch {
      return 'dark';
    }
  });
  const menuRef = useRef<HTMLDivElement>(null);
  const langRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    setUser(getStoredUser());
    setMenuOpen(false);
    setSidebarOpen(false);
    setLangOpen(false);
  }, [location.pathname]);

  useEffect(() => {
    try {
      localStorage.setItem(THEME_KEY, theme);
    } catch {
      /* ignore */
    }
    document.documentElement.classList.toggle('light', theme === 'light');
    document.documentElement.classList.toggle('dark', theme === 'dark');
  }, [theme]);

  useEffect(() => {
    function onClick(e: MouseEvent) {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) setMenuOpen(false);
      if (langRef.current && !langRef.current.contains(e.target as Node)) setLangOpen(false);
    }
    document.addEventListener('mousedown', onClick);
    return () => document.removeEventListener('mousedown', onClick);
  }, []);

  const username = user?.username ?? 'admin';
  const role = typeof user?.role === 'string' ? user.role : 'admin';

  function onLogout() {
    api.logout();
    navigate('/login', { replace: true });
  }

  const activeNav = NAV.find((n) => (n.end ? location.pathname === n.to : location.pathname.startsWith(n.to)));

  const sidebar = (
    <aside className="flex h-full w-64 flex-col border-r border-zinc-800/80 bg-zinc-950">
      {/* Logo */}
      <div className="flex h-16 items-center gap-2 border-b border-zinc-800/80 px-5">
        <Logo size={26} withWordmark={false} />
        <div className="leading-tight">
          <div className="text-[15px] font-bold tracking-tight text-zinc-50">
            GPU<span className="text-emerald-400">Hub</span>
          </div>
          <div className="text-[10px] font-medium uppercase tracking-[0.18em] text-zinc-500">
            {L(lang, { en: 'Orchestration', tr: 'Orkestrasyon' })}
          </div>
        </div>
      </div>

      {/* Nav */}
      <nav className="flex-1 space-y-0.5 overflow-y-auto px-3 py-4">
        <div className="px-2 pb-2 text-[10px] font-semibold uppercase tracking-[0.2em] text-zinc-600">
          {L(lang, { en: 'Platform', tr: 'Platform' })}
        </div>
        {NAV.map(({ to, en, tr, icon: Icon, end }) => (
          <NavLink
            key={to}
            to={to}
            end={end}
            className={({ isActive }) =>
              `group flex items-center gap-3 rounded-lg border border-transparent px-3 py-2 text-[13px] font-medium transition-all ${
                isActive
                  ? 'border-emerald-500/20 bg-emerald-500/[0.08] text-emerald-300'
                  : 'text-zinc-400 hover:bg-zinc-900 hover:text-zinc-100'
              }`
            }
          >
            {({ isActive }) => (
              <>
                <Icon size={17} strokeWidth={1.8} className={isActive ? 'text-emerald-400' : 'text-zinc-500 group-hover:text-zinc-300'} />
                {L(lang, { en, tr })}
                {isActive && <span className="ml-auto h-1.5 w-1.5 rounded-full bg-emerald-400" />}
              </>
            )}
          </NavLink>
        ))}
      </nav>

      {/* Footer hint */}
      <div className="border-t border-zinc-800/80 px-4 py-3">
        <div className="flex items-center gap-2 rounded-lg border border-zinc-800 bg-zinc-900/50 px-3 py-2">
          <ShieldCheck size={14} className="shrink-0 text-emerald-500/80" />
          <div className="min-w-0">
            <div className="text-[11px] font-medium text-zinc-300">
              {L(lang, { en: 'Self-hosted', tr: 'Self-hosted' })}
            </div>
            <div className="truncate text-[10px] text-zinc-500">
              {L(lang, { en: 'Free-tier GPU orchestration', tr: 'Ücretsiz GPU orkestrasyonu' })}
            </div>
          </div>
        </div>
      </div>
    </aside>
  );

  return (
    <div className="flex h-screen overflow-hidden bg-zinc-950">
      {/* Desktop sidebar */}
      <div className="hidden shrink-0 lg:block">{sidebar}</div>

      {/* Mobile sidebar overlay */}
      {sidebarOpen && (
        <div className="fixed inset-0 z-40 lg:hidden">
          <div className="absolute inset-0 bg-black/70" onClick={() => setSidebarOpen(false)} />
          <div className="relative h-full w-64">{sidebar}</div>
          <button
            onClick={() => setSidebarOpen(false)}
            className="absolute left-[272px] top-4 rounded-md border border-zinc-800 bg-zinc-900 p-2 text-zinc-400"
            aria-label={L(lang, { en: 'Close menu', tr: 'Menüyü kapat' })}
          >
            <X size={16} />
          </button>
        </div>
      )}

      {/* Main column */}
      <div className="flex min-w-0 flex-1 flex-col">
        {/* Top bar */}
        <header className="flex h-16 shrink-0 items-center gap-3 border-b border-zinc-800/80 bg-zinc-950/80 px-4 backdrop-blur sm:px-6">
          <button
            className="rounded-md p-2 text-zinc-400 hover:bg-zinc-900 hover:text-zinc-100 lg:hidden"
            onClick={() => setSidebarOpen(true)}
            aria-label={L(lang, { en: 'Open menu', tr: 'Menüyü aç' })}
          >
            <Menu size={18} />
          </button>

          <div className="hidden items-center gap-2 text-xs text-zinc-500 md:flex">
            <span className="font-medium text-zinc-400">GPUHub</span>
            <span>/</span>
            <span className="text-zinc-300">{activeNav ? L(lang, { en: activeNav.en, tr: activeNav.tr }) : 'Console'}</span>
          </div>

          <div className="ml-auto flex items-center gap-2">
            {/* Language switcher */}
            <div className="relative" ref={langRef}>
              <button
                onClick={() => setLangOpen((v) => !v)}
                className="flex items-center gap-1.5 rounded-lg border border-zinc-800 bg-zinc-900 px-2.5 py-2 text-xs font-semibold text-zinc-300 transition-colors hover:border-zinc-700 hover:bg-zinc-800"
                aria-label={L(lang, { en: 'Switch language', tr: 'Dili değiştir' })}
              >
                <Globe size={15} />
                <span className="uppercase">{lang}</span>
                <ChevronDown size={12} className={`text-zinc-500 transition-transform ${langOpen ? 'rotate-180' : ''}`} />
              </button>
              {langOpen && (
                <div className="absolute right-0 z-30 mt-2 w-44 overflow-hidden rounded-xl border border-zinc-800 bg-zinc-900 py-1 shadow-xl shadow-black/50">
                  {(['en', 'tr'] as Lang[]).map((l) => (
                    <button
                      key={l}
                      onClick={() => { setLang(l); setLangOpen(false); }}
                      className={`flex w-full items-center justify-between px-4 py-2.5 text-sm transition-colors hover:bg-zinc-800 ${lang === l ? 'text-emerald-400' : 'text-zinc-300'}`}
                    >
                      <span>{l === 'en' ? 'English' : 'Türkçe'}</span>
                      {lang === l && <span className="h-1.5 w-1.5 rounded-full bg-emerald-400" />}
                    </button>
                  ))}
                </div>
              )}
            </div>

            {/* Theme toggle */}
            <button
              onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}
              className="rounded-lg border border-zinc-800 bg-zinc-900 p-2 text-zinc-300 transition-colors hover:border-zinc-700 hover:bg-zinc-800 hover:text-zinc-100"
              aria-label={L(lang, { en: 'Toggle theme', tr: 'Tema değiştir' })}
              title={L(lang, { en: theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme', tr: theme === 'dark' ? 'Açık temaya geç' : 'Koyu temaya geç' })}
            >
              {theme === 'dark' ? <Sun size={17} /> : <Moon size={17} />}
            </button>

            {/* User menu */}
            <div className="relative" ref={menuRef}>
              <button
                onClick={() => setMenuOpen((v) => !v)}
                className="flex items-center gap-2 rounded-lg border border-zinc-800 bg-zinc-900 py-1.5 pl-1.5 pr-2 transition-colors hover:border-zinc-700 hover:bg-zinc-800"
              >
                <span className="flex h-7 w-7 items-center justify-center rounded-md border border-emerald-500/30 bg-emerald-500/10 text-[11px] font-bold text-emerald-400">
                  {initials(username)}
                </span>
                <span className="hidden max-w-[120px] truncate text-xs font-medium text-zinc-200 sm:block">
                  {username}
                </span>
                <ChevronDown size={14} className={`text-zinc-500 transition-transform ${menuOpen ? 'rotate-180' : ''}`} />
              </button>

              {menuOpen && (
                <div className="absolute right-0 z-30 mt-2 w-56 overflow-hidden rounded-xl border border-zinc-800 bg-zinc-900 shadow-xl shadow-black/50">
                  <div className="border-b border-zinc-800 px-4 py-3">
                    <div className="flex items-center gap-2 text-sm font-medium text-zinc-100">
                      <UserRound size={14} className="text-zinc-500" />
                      {username}
                    </div>
                    <div className="mt-0.5 pl-6 text-[11px] uppercase tracking-wider text-zinc-500">role: {role}</div>
                  </div>
                  <button
                    onClick={onLogout}
                    className="flex w-full items-center gap-2 px-4 py-2.5 text-sm text-red-400 transition-colors hover:bg-red-500/10"
                  >
                    <LogOut size={15} />
                    {L(lang, { en: 'Sign out', tr: 'Çıkış yap' })}
                  </button>
                </div>
              )}
            </div>
          </div>
        </header>

        {/* Content */}
        <main className="min-h-0 flex-1 overflow-y-auto">
          <div className="mx-auto max-w-[1400px] px-4 py-6 sm:px-6">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  );
}
