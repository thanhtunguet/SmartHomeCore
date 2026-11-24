/** "3 min ago" style label; `iso` undefined means the event never happened. */
export function formatRelative(iso: string | undefined, now = Date.now()): string {
  if (!iso) return 'Never';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return 'Never';
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 10) return 'Just now';
  if (s < 60) return `${s}s ago`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m} min ago`;
  const h = Math.round(m / 60);
  if (h < 48) return `${h} h ago`;
  return `${Math.round(h / 24)} d ago`;
}

export function clockTime(d = new Date()): string {
  return d.toLocaleTimeString();
}
