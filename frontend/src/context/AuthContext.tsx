import React, { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import { ApiError, setAuthLostHandler } from '../api/client';
import * as api from '../api/endpoints';
import { clearSession, onSessionChange, readSession, writeSession } from '../api/session';
import type { User } from '../api/schema';

type AuthStatus = 'loading' | 'anonymous' | 'authenticated';

interface AuthContextType {
  status: AuthStatus;
  user: User | null;
  /** Why the user is on the login screen (expired session, controller unreachable, ...). */
  notice: string | null;
  login: (username: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthContextType | undefined>(undefined);

export const AuthProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [status, setStatus] = useState<AuthStatus>('loading');
  const [user, setUser] = useState<User | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const drop = useCallback((message: string | null) => {
    setUser(null);
    setStatus('anonymous');
    setNotice(message);
  }, []);

  // Restore a stored session.
  useEffect(() => {
    if (!readSession()) {
      setStatus('anonymous');
      return;
    }
    let cancelled = false;
    api
      .getCurrentUser()
      .then(u => {
        if (cancelled) return;
        setUser(u);
        setStatus('authenticated');
      })
      .catch(e => {
        if (cancelled) return;
        if (e instanceof ApiError && !e.unauthorized) {
          // Keep the stored session: the Controller may just be restarting.
          drop(e.notImplemented ? 'The Controller does not implement /auth/me yet (501).' : e.message);
        } else {
          drop(null);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [drop]);

  // The client could not refresh the token, or another tab logged out.
  useEffect(() => {
    setAuthLostHandler(() => drop('Your session expired. Please sign in again.'));
    const off = onSessionChange(s => {
      if (!s) drop(null);
    });
    return () => {
      setAuthLostHandler(null);
      off();
    };
  }, [drop]);

  const login = useCallback(async (username: string, password: string) => {
    const pair = await api.login(username, password);
    writeSession(pair);
    try {
      setUser(await api.getCurrentUser());
    } catch (e) {
      clearSession();
      throw e;
    }
    setNotice(null);
    setStatus('authenticated');
  }, []);

  const logout = useCallback(async () => {
    const s = readSession();
    if (s) {
      try {
        await api.logout(s.refreshToken);
      } catch {
        /* best effort: the local session is dropped regardless */
      }
    }
    clearSession();
    drop(null);
  }, [drop]);

  const value = useMemo<AuthContextType>(
    () => ({ status, user, notice, login, logout }),
    [status, user, notice, login, logout],
  );
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
};

export const useAuth = () => {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useAuth must be used within an AuthProvider');
  return ctx;
};
