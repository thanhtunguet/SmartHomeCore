import { useCallback, useRef, useState } from 'react';
import { ApiError } from '../../api/client';
import { listCloudflareRecords, listCloudflareZones } from '../../api/endpoints';
import type { CloudflareLookup, CloudflareRecord, CloudflareZone } from '../../api/schema';

/** What the user is told when Cloudflare could not be queried; the server's own message is kept. */
export function lookupFailure(e: unknown): string {
  if (e instanceof ApiError) {
    return e.network ? e.message : `${e.message} (HTTP ${e.status})`;
  }
  return e instanceof Error ? e.message : String(e);
}

type Attempt = { kind: 'zones'; lookup: CloudflareLookup } | { kind: 'records'; zoneId: string; lookup: CloudflareLookup };

/**
 * State behind the DDNS zone/record dropdowns, the only way to choose a zone and a record. A
 * failed lookup (501 until the Controller has its Cloudflare provider, 502 when Cloudflare rejects
 * the token, network, ...) leaves the server's message in `error`; `retry()` repeats the last
 * lookup. Late answers of superseded lookups are ignored.
 */
export function useCloudflareLookup() {
  const [zones, setZones] = useState<CloudflareZone[]>([]);
  const [records, setRecords] = useState<CloudflareRecord[]>([]);
  const [loadingZones, setLoadingZones] = useState(false);
  const [loadingRecords, setLoadingRecords] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const run = useRef(0);
  const last = useRef<Attempt | null>(null);

  /** Forget everything (another record selected, token changed, ...). */
  const reset = useCallback(() => {
    run.current++;
    last.current = null;
    setZones([]);
    setRecords([]);
    setLoadingZones(false);
    setLoadingRecords(false);
    setError(null);
  }, []);

  const loadZones = useCallback(async (lookup: CloudflareLookup) => {
    const mine = ++run.current;
    last.current = { kind: 'zones', lookup };
    setLoadingZones(true);
    setLoadingRecords(false);
    setError(null);
    setRecords([]);
    try {
      const items = await listCloudflareZones(lookup);
      if (run.current !== mine) return;
      setZones(items);
      if (items.length === 0) setError('The token can see no Cloudflare zones.');
    } catch (e) {
      if (run.current !== mine) return;
      setError(lookupFailure(e));
    } finally {
      if (run.current === mine) setLoadingZones(false);
    }
  }, []);

  const loadRecords = useCallback(async (zoneId: string, lookup: CloudflareLookup) => {
    const mine = ++run.current;
    last.current = { kind: 'records', zoneId, lookup };
    setLoadingRecords(true);
    setError(null);
    setRecords([]);
    try {
      const items = await listCloudflareRecords(zoneId, lookup);
      if (run.current !== mine) return;
      setRecords(items);
      if (items.length === 0) setError('This zone has no A or AAAA records. Create one in Cloudflare first.');
    } catch (e) {
      if (run.current !== mine) return;
      setError(lookupFailure(e));
    } finally {
      if (run.current === mine) setLoadingRecords(false);
    }
  }, []);

  /** Repeat the lookup that failed (or just ran). */
  const retry = useCallback(() => {
    const a = last.current;
    if (!a) return;
    if (a.kind === 'zones') void loadZones(a.lookup);
    else void loadRecords(a.zoneId, a.lookup);
  }, [loadZones, loadRecords]);

  return { zones, records, loadingZones, loadingRecords, error, reset, loadZones, loadRecords, retry };
}
