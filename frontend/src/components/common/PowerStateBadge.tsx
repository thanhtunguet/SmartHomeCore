import React from 'react';
import { Device } from '../../types';

interface PowerStateBadgeProps {
  status: Device['status'];
  transition: Device['powerTransition'];
}

/** ON / OFF badge with a dot; shows the transition while a wake or shutdown is in flight. */
export const PowerStateBadge: React.FC<PowerStateBadgeProps> = ({
  status,
  transition
}) => {
  let label: string;
  let tone: string;
  let dot: string;
  let pulse = false;
  if (transition === 'waking') {
    label = 'Waking';
    tone = 'text-amber-700 dark:text-amber-300 bg-amber-500/10 border-amber-500/20';
    dot = 'bg-amber-500';
    pulse = true;
  } else if (transition === 'shutting_down') {
    label = 'Shutting down';
    tone = 'text-amber-700 dark:text-amber-300 bg-amber-500/10 border-amber-500/20';
    dot = 'bg-amber-500';
    pulse = true;
  } else if (status === 'online') {
    label = 'ON';
    tone = 'text-emerald-700 dark:text-emerald-300 bg-emerald-500/10 border-emerald-500/20';
    dot = 'bg-emerald-500';
  } else if (status === 'offline') {
    label = 'OFF';
    tone = 'text-neutral-600 dark:text-neutral-300 bg-neutral-500/10 border-neutral-500/20';
    dot = 'bg-neutral-400 dark:bg-neutral-500';
  } else {
    label = 'Unknown';
    tone = 'text-neutral-500 dark:text-neutral-400 bg-neutral-500/10 border-neutral-500/20';
    dot = 'bg-neutral-300 dark:bg-neutral-600';
  }
  return (
    <span
      role="status"
      className={`inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap px-2 py-0.5 rounded-full border text-[11px] font-semibold normal-case tracking-normal ${tone}`}
    >
      <span className={`size-1.5 rounded-full ${dot} ${pulse ? 'animate-pulse' : ''}`} aria-hidden="true" />
      <span>{label}</span>
    </span>
  );
};
