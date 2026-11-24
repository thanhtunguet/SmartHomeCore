import React, { useEffect, useState } from 'react';
import { Server, LogIn, AlertCircle, Loader2 } from 'lucide-react';
import { useAuth } from '../../context/AuthContext';
import { ApiError } from '../../api/client';
import { getHealthz } from '../../api/endpoints';
import type { Health } from '../../api/schema';

export const LoginScreen: React.FC = () => {
  const { login, notice } = useAuth();
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [health, setHealth] = useState<Health | null>(null);
  const [healthError, setHealthError] = useState(false);

  useEffect(() => {
    getHealthz()
      .then(setHealth)
      .catch(() => setHealthError(true));
  }, []);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      await login(username.trim(), password);
    } catch (err) {
      if (err instanceof ApiError) {
        setError(
          err.notImplemented
            ? 'Sign-in is not implemented on this Controller yet (POST /api/v1/auth/login returned 501).'
            : err.unauthorized
              ? 'Wrong username or password.'
              : err.message,
        );
      } else {
        setError('Unexpected error while signing in.');
      }
    } finally {
      setBusy(false);
    }
  };

  const inputCls =
    'w-full px-3 py-2 bg-neutral-50 dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 rounded-lg text-neutral-900 dark:text-neutral-100 text-sm focus:border-emerald-500 focus:outline-hidden';

  return (
    <div className="min-h-screen flex items-center justify-center p-4 bg-neutral-100 dark:bg-neutral-950">
      <form
        onSubmit={submit}
        className="w-full max-w-sm bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl p-6 shadow-xs space-y-4"
      >
        <div className="flex items-center gap-3">
          <div className="p-2.5 rounded-xl bg-emerald-500/10 text-emerald-600 dark:text-emerald-400">
            <Server size={22} />
          </div>
          <div>
            <h1 className="text-base font-semibold text-neutral-900 dark:text-neutral-100">HomeCore</h1>
            <p className="text-xs text-neutral-500 dark:text-neutral-400">Sign in to the Controller</p>
          </div>
        </div>

        {(notice || error) && (
          <div role="alert" className="flex items-start gap-2 p-3 rounded-lg text-xs bg-amber-500/10 text-amber-700 dark:text-amber-300 border border-amber-500/20">
            <AlertCircle size={14} className="shrink-0 mt-0.5" />
            <span>{error ?? notice}</span>
          </div>
        )}

        <label className="block text-xs font-medium text-neutral-700 dark:text-neutral-300 space-y-1">
          <span>Username</span>
          <input
            className={inputCls}
            value={username}
            onChange={e => setUsername(e.target.value)}
            autoComplete="username"
            maxLength={64}
            required
            autoFocus
          />
        </label>
        <label className="block text-xs font-medium text-neutral-700 dark:text-neutral-300 space-y-1">
          <span>Password</span>
          <input
            className={inputCls}
            type="password"
            value={password}
            onChange={e => setPassword(e.target.value)}
            autoComplete="current-password"
            maxLength={256}
            required
          />
        </label>

        <button
          type="submit"
          disabled={busy || !username || !password}
          className="w-full flex items-center justify-center gap-2 px-3 py-2 text-sm font-medium text-white bg-emerald-600 hover:bg-emerald-500 rounded-lg transition-colors disabled:opacity-50"
        >
          {busy ? <Loader2 size={16} className="animate-spin" /> : <LogIn size={16} />}
          <span>{busy ? 'Signing in…' : 'Sign in'}</span>
        </button>

        <p className="text-[11px] text-neutral-400 text-center">
          {health
            ? `Controller v${health.version} · ${health.status}`
            : healthError
              ? 'Controller unreachable'
              : 'Contacting Controller…'}
        </p>
      </form>
    </div>
  );
};
