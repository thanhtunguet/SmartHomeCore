import React from 'react';
import { X } from 'lucide-react';
import { useApp } from '../../context/AppContext';

export const Toasts: React.FC = () => {
  const { toasts, dismissToast } = useApp();
  if (toasts.length === 0) return null;
  return (
    <div className="fixed bottom-20 md:bottom-6 right-4 z-[60] flex flex-col gap-2 max-w-sm" aria-live="polite">
      {toasts.map(t => (
        <div
          key={t.id}
          role={t.kind === 'error' ? 'alert' : 'status'}
          className={`flex items-start gap-2 px-3.5 py-2.5 rounded-lg text-xs shadow-lg border ${
            t.kind === 'error'
              ? 'bg-red-50 dark:bg-red-950 text-red-800 dark:text-red-200 border-red-200 dark:border-red-900'
              : 'bg-white dark:bg-neutral-900 text-neutral-800 dark:text-neutral-200 border-neutral-200 dark:border-neutral-800'
          }`}
        >
          <span className="flex-1">{t.message}</span>
          <button onClick={() => dismissToast(t.id)} aria-label="Dismiss" className="opacity-60 hover:opacity-100">
            <X size={13} />
          </button>
        </div>
      ))}
    </div>
  );
};
