import React from 'react';
import { DeviceStatus } from '../../types';
import { CheckCircle2, XCircle, HelpCircle } from 'lucide-react';

interface StatusBadgeProps {
  status: DeviceStatus;
  showText?: boolean;
  className?: string;
  size?: 'sm' | 'md';
}

export const StatusBadge: React.FC<StatusBadgeProps> = ({
  status,
  showText = true,
  className = '',
  size = 'md'
}) => {
  const iconSize = size === 'sm' ? 12 : 14;

  if (status === 'online') {
    return (
      <span className={`inline-flex items-center gap-1.5 font-medium text-emerald-600 dark:text-emerald-400 ${size === 'sm' ? 'text-xs' : 'text-xs'} ${className}`}>
        <CheckCircle2 size={iconSize} className="shrink-0 text-emerald-500" />
        {showText && <span>Online</span>}
      </span>
    );
  }

  if (status === 'offline') {
    return (
      <span className={`inline-flex items-center gap-1.5 font-medium text-neutral-500 dark:text-neutral-400 ${size === 'sm' ? 'text-xs' : 'text-xs'} ${className}`}>
        <XCircle size={iconSize} className="shrink-0 text-neutral-400 dark:text-neutral-500" />
        {showText && <span>Offline</span>}
      </span>
    );
  }

  return (
    <span className={`inline-flex items-center gap-1.5 font-medium text-amber-600 dark:text-amber-400 ${size === 'sm' ? 'text-xs' : 'text-xs'} ${className}`}>
      <HelpCircle size={iconSize} className="shrink-0 text-amber-500" />
      {showText && <span>Unknown</span>}
    </span>
  );
};
