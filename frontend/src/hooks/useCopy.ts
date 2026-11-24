import { useCallback, useEffect, useRef, useState } from 'react';

/**
 * Copy text to the clipboard and remember for two seconds which item was copied (`copied` is the key
 * given to `copy`). `failed` is set when the Clipboard API is unavailable (plain HTTP on a LAN
 * address): the text is selectable, so the user can copy it by hand.
 */
export const useCopy = () => {
  const [copied, setCopied] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);
  const timer = useRef<number | undefined>(undefined);

  useEffect(() => () => window.clearTimeout(timer.current), []);

  const copy = useCallback(async (key: string, text: string) => {
    window.clearTimeout(timer.current);
    try {
      await navigator.clipboard.writeText(text);
      setCopied(key);
      setFailed(false);
    } catch {
      setCopied(null);
      setFailed(true);
    }
    timer.current = window.setTimeout(() => {
      setCopied(null);
      setFailed(false);
    }, 2000);
  }, []);

  return { copied, failed, copy };
};
