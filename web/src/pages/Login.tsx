import { useEffect, useState, type FormEvent } from 'react';
import { useNavigate } from 'react-router-dom';
import { Eye, EyeOff, Loader2, ShieldCheck, UserPlus, LogIn, Globe } from 'lucide-react';
import { api, errorMessage, getAccessToken } from '../api';
import Logo from '../components/Logo';
import { Button, Input, Label, FieldError } from '../components/ui';
import { useToast } from '../components/Toast';
import { useLang, useSetLang, L, type Lang } from '../i18n';

export default function Login() {
  const navigate = useNavigate();
  const toast = useToast();
  const lang = useLang();
  const setLang = useSetLang();

  const [needsSetup, setNeedsSetup] = useState<boolean | null>(null);
  const [statusError, setStatusError] = useState('');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [showPassword, setShowPassword] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState('');

  useEffect(() => {
    if (getAccessToken()) {
      navigate('/', { replace: true });
      return;
    }
    let cancelled = false;
    api
      .authStatus()
      .then((d) => {
        if (!cancelled) setNeedsSetup(!!d?.needs_setup);
      })
      .catch((e) => {
        if (!cancelled) {
          setNeedsSetup(false);
          setStatusError(errorMessage(e, lang === 'tr' ? 'GPUHub API’ye ulaşılamadı' : 'Could not reach the GPUHub API'));
        }
      });
    return () => {
      cancelled = true;
    };
  }, [navigate, lang]);

  const isSetup = needsSetup === true;

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setFormError('');

    if (!username.trim() || !password) {
      setFormError(L(lang, { en: 'Username and password are required.', tr: 'Kullanıcı adı ve şifre gerekli.' }));
      return;
    }
    if (isSetup && password !== confirm) {
      setFormError(L(lang, { en: 'Passwords do not match.', tr: 'Şifreler eşleşmiyor.' }));
      return;
    }
    if (isSetup && password.length < 8) {
      setFormError(L(lang, { en: 'Password must be at least 8 characters.', tr: 'Şifre en az 8 karakter olmalı.' }));
      return;
    }

    setSubmitting(true);
    try {
      if (isSetup) {
        await api.setup({ username: username.trim(), password, confirm });
        toast(L(lang, { en: 'Admin account created - please sign in', tr: 'Admin hesabı oluşturuldu - giriş yapın' }), 'success');
        setNeedsSetup(false);
        setPassword('');
        setConfirm('');
        setFormError('');
      } else {
        await api.login(username.trim(), password);
        toast(L(lang, { en: `Welcome back, ${username.trim()}`, tr: `Tekrar hoş geldiniz, ${username.trim()}` }), 'success');
        navigate('/', { replace: true });
      }
    } catch (err) {
      setFormError(
        errorMessage(err, isSetup ? L(lang, { en: 'Setup failed', tr: 'Kurulum başarısız' }) : L(lang, { en: 'Invalid credentials', tr: 'Geçersiz kullanıcı adı/şifre' })),
      );
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="relative flex min-h-screen items-center justify-center overflow-hidden bg-zinc-950 px-4">
      {/* Language toggle (top-right) */}
      <button
        onClick={() => setLang(lang === 'en' ? 'tr' : 'en')}
        className="absolute right-4 top-4 z-10 flex items-center gap-1.5 rounded-lg border border-zinc-800 bg-zinc-900/80 px-2.5 py-2 text-xs font-semibold text-zinc-400 transition-colors hover:border-zinc-700 hover:text-zinc-100"
        aria-label="Switch language"
      >
        <Globe size={14} />
        <span className="uppercase">{lang}</span>
      </button>

      {/* Ambient background */}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0"
        style={{
          background:
            'radial-gradient(ellipse 80% 60% at 50% -10%, rgba(16,185,129,0.14), transparent), radial-gradient(ellipse 60% 50% at 80% 110%, rgba(34,211,238,0.08), transparent)',
        }}
      />
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 opacity-[0.35]"
        style={{
          backgroundImage:
            'linear-gradient(rgba(255,255,255,0.025) 1px, transparent 1px), linear-gradient(90deg, rgba(255,255,255,0.025) 1px, transparent 1px)',
          backgroundSize: '44px 44px',
        }}
      />

      <div className="relative w-full max-w-md">
        {/* Brand */}
        <div className="mb-8 flex flex-col items-center text-center">
          <div className="mb-4">
            <Logo size={56} withWordmark={false} />
          </div>
          <h1 className="text-2xl font-bold tracking-tight text-zinc-50">
            GPU<span className="text-emerald-400">Hub</span>
          </h1>
          <p className="mt-1.5 max-w-xs text-sm text-zinc-500">
            {L(lang, {
              en: 'Self-hosted orchestration for free-tier GPU compute providers',
              tr: 'Ücretsiz GPU sağlayıcıları için self-hosted orkestrasyon',
            })}
          </p>
        </div>

        {/* Card */}
        <div className="rounded-2xl border border-zinc-800 bg-zinc-900/60 p-6 shadow-2xl shadow-black/50 backdrop-blur sm:p-7">
          {needsSetup === null ? (
            <div className="flex h-44 flex-col items-center justify-center gap-3 text-sm text-zinc-500">
              <Loader2 className="animate-spin text-emerald-500" size={22} />
              {L(lang, { en: 'Checking instance status…', tr: 'Sunucu durumu kontrol ediliyor…' })}
            </div>
          ) : (
            <>
              <div className="mb-5 flex items-center gap-3">
                <div className="flex h-9 w-9 items-center justify-center rounded-lg border border-zinc-800 bg-zinc-900">
                  {isSetup ? (
                    <UserPlus size={17} className="text-emerald-400" />
                  ) : (
                    <ShieldCheck size={17} className="text-emerald-400" />
                  )}
                </div>
                <div>
                  <h2 className="text-sm font-semibold text-zinc-100">
                    {isSetup
                      ? L(lang, { en: 'Create admin account', tr: 'Admin hesabı oluştur' })
                      : L(lang, { en: 'Sign in to GPUHub', tr: 'GPUHub’a giriş yap' })}
                  </h2>
                  <p className="text-xs text-zinc-500">
                    {isSetup
                      ? L(lang, {
                          en: 'First run - no admin exists yet. This account takes ownership.',
                          tr: 'İlk çalıştırma - henüz admin yok. Bu hesap sahipliği alır.',
                        })
                      : L(lang, {
                          en: 'Use your GPUHub credentials to continue.',
                          tr: 'Devam etmek için GPUHub credential’ınızı kullanın.',
                        })}
                  </p>
                </div>
              </div>

              {statusError && (
                <div className="mb-4 rounded-lg border border-amber-500/25 bg-amber-500/5 px-3 py-2 text-xs text-amber-300">
                  {statusError}
                </div>
              )}

              <form onSubmit={onSubmit} className="space-y-4">
                <div>
                  <Label htmlFor="username">{L(lang, { en: 'Username', tr: 'Kullanıcı adı' })}</Label>
                  <Input
                    id="username"
                    autoComplete="username"
                    autoFocus
                    placeholder="admin"
                    value={username}
                    onChange={(e) => setUsername(e.target.value)}
                    disabled={submitting}
                  />
                </div>

                <div>
                  <Label htmlFor="password">{L(lang, { en: 'Password', tr: 'Şifre' })}</Label>
                  <div className="relative">
                    <Input
                      id="password"
                      type={showPassword ? 'text' : 'password'}
                      autoComplete={isSetup ? 'new-password' : 'current-password'}
                      placeholder="••••••••••"
                      value={password}
                      onChange={(e) => setPassword(e.target.value)}
                      disabled={submitting}
                      className="pr-10"
                    />
                    <button
                      type="button"
                      onClick={() => setShowPassword((v) => !v)}
                      className="absolute right-2.5 top-1/2 -translate-y-1/2 rounded p-1 text-zinc-500 hover:text-zinc-200"
                      aria-label={showPassword ? 'Hide password' : 'Show password'}
                    >
                      {showPassword ? <EyeOff size={16} /> : <Eye size={16} />}
                    </button>
                  </div>
                </div>

                {isSetup && (
                  <div>
                    <Label htmlFor="confirm">{L(lang, { en: 'Confirm password', tr: 'Şifreyi tekrarla' })}</Label>
                    <Input
                      id="confirm"
                      type={showPassword ? 'text' : 'password'}
                      autoComplete="new-password"
                      placeholder="••••••••••"
                      value={confirm}
                      onChange={(e) => setConfirm(e.target.value)}
                      disabled={submitting}
                    />
                  </div>
                )}

                <FieldError>{formError}</FieldError>

                <Button type="submit" variant="primary" className="w-full" loading={submitting}>
                  {isSetup ? (
                    <>
                      <UserPlus size={16} /> {L(lang, { en: 'Create admin', tr: 'Admin oluştur' })}
                    </>
                  ) : (
                    <>
                      <LogIn size={16} /> {L(lang, { en: 'Sign in', tr: 'Giriş yap' })}
                    </>
                  )}
                </Button>
              </form>
            </>
          )}
        </div>

        <p className="mt-6 text-center text-[11px] text-zinc-600">
          {L(lang, {
            en: 'GPUHub is self-hosted. Credentials never leave your infrastructure.',
            tr: 'GPUHub self-hosted’dır. Credential’larınız altyapınızdan asla çıkmaz.',
          })}
        </p>
      </div>
    </div>
  );
}
