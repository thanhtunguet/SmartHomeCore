/**
 * Legacy, browser-local device type/OS (GAP-API-01).
 *
 * The Controller now stores `device_type` and `os` on the device. Earlier builds of this UI kept
 * them in localStorage, keyed by device id. This module only exists to carry those values over
 * once: `migrateLegacyDeviceMeta` pushes each entry to the server and then deletes it. It can be
 * removed once nobody runs a pre-migration build.
 */
import type { DeviceType, OperatingSystem } from '../types';

interface LegacyMeta {
  type: DeviceType;
  os: OperatingSystem;
}

const KEY = 'homecore_device_meta_v1';

function readAll(): Record<string, LegacyMeta> {
  try {
    return JSON.parse(localStorage.getItem(KEY) ?? '{}') as Record<string, LegacyMeta>;
  } catch {
    return {};
  }
}

function writeAll(all: Record<string, LegacyMeta>): void {
  try {
    if (Object.keys(all).length === 0) localStorage.removeItem(KEY);
    else localStorage.setItem(KEY, JSON.stringify(all));
  } catch {
    /* storage unavailable: nothing to migrate anyway */
  }
}

/**
 * For every locally stored entry: drop it if the device no longer exists or already matches the
 * server, otherwise send it with `push` and drop it after success. A failed push keeps the entry
 * for the next load. Any authenticated session may do it (there are no roles).
 */
export async function migrateLegacyDeviceMeta(
  devices: ReadonlyArray<{ id: string; device_type?: DeviceType; os?: OperatingSystem }>,
  push: (id: string, meta: { device_type: DeviceType; os: OperatingSystem }) => Promise<unknown>
): Promise<number> {
  const all = readAll();
  const byId = new Map(devices.map(d => [d.id, d]));
  let moved = 0;
  for (const [id, meta] of Object.entries(all)) {
    const d = byId.get(id);
    if (d && (d.device_type !== meta.type || d.os !== meta.os)) {
      try {
        await push(id, { device_type: meta.type, os: meta.os });
        moved++;
      } catch {
        continue; // keep it, retry on the next load
      }
    }
    delete all[id];
  }
  writeAll(all);
  return moved;
}
