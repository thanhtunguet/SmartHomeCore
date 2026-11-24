import { describe, expect, it } from 'vitest';
import { ALL_EVENTS, deliveryState, draftFromChannel, emptyDraft, toggleEvent, validateDraft } from './alerts';
import type { AlertChannel } from './schema';

const TOKEN = '123456789:AAH-test_token_0123456789abcdefghij';
const draft = (over: Partial<ReturnType<typeof emptyDraft>> = {}) => ({ ...emptyDraft(), name: 'Phone', targetUserId: '424242', token: TOKEN, ...over });

describe('validateDraft', () => {
  it('accepts a complete draft and trims it', () => {
    expect(validateDraft(draft({ name: '  Phone ', targetUserId: ' 424242 ', token: ` ${TOKEN} ` }), false)).toEqual({
      value: { name: 'Phone', targetUserId: '424242', token: TOKEN, events: ALL_EVENTS },
    });
  });

  it('accepts a negative id (group) and refuses non-numeric ids', () => {
    expect(validateDraft(draft({ targetUserId: '-1001234567890' }), false)).toHaveProperty('value');
    for (const id of ['', '@me', '12a', '1.5']) expect(validateDraft(draft({ targetUserId: id }), false), id).toHaveProperty('error');
  });

  it('requires a well-formed token on create, and allows a blank one when editing (keeps the stored token)', () => {
    expect(validateDraft(draft({ token: '' }), false)).toHaveProperty('error');
    expect(validateDraft(draft({ token: 'not-a-token' }), false)).toHaveProperty('error');
    expect(validateDraft(draft({ token: '' }), true)).toHaveProperty('value');
    expect(validateDraft(draft({ token: 'not-a-token' }), true)).toHaveProperty('error');
  });

  it('refuses an empty or control-character name and no events', () => {
    expect(validateDraft(draft({ name: '   ' }), false)).toHaveProperty('error');
    expect(validateDraft(draft({ name: 'a\nb' }), false)).toHaveProperty('error');
    expect(validateDraft(draft({ name: 'x'.repeat(101) }), false)).toHaveProperty('error');
    expect(validateDraft(draft({ events: [] }), false)).toHaveProperty('error');
  });

  it('the error never echoes the token', () => {
    const r = validateDraft(draft({ token: '987654321:short' }), false);
    expect(JSON.stringify(r)).not.toContain('987654321');
  });
});

describe('toggleEvent', () => {
  it('adds and removes, keeping documentation order', () => {
    expect(toggleEvent(['status_changed'], 'ip_changed')).toEqual(['ip_changed', 'status_changed']);
    expect(toggleEvent(ALL_EVENTS, 'power_command')).toEqual(['ip_changed', 'status_changed']);
  });
});

describe('draftFromChannel', () => {
  it('fills the form from the channel and leaves the token blank', () => {
    const c = { name: 'Phone', enabled: false, events: ['ip_changed'], settings: { target_user_id: '7' } } as unknown as AlertChannel;
    expect(draftFromChannel(c)).toEqual({ name: 'Phone', targetUserId: '7', token: '', events: ['ip_changed'], enabled: false });
  });
});

describe('deliveryState', () => {
  const t = (s: string) => `2026-11-15T${s}Z`;
  it('paused beats everything', () => {
    expect(deliveryState({ enabled: false, last_error: 'x', last_error_at: t('10:00:00') }).tone).toBe('off');
  });
  it('never used, delivered, failed', () => {
    expect(deliveryState({ enabled: true }).label).toBe('Nothing sent yet');
    expect(deliveryState({ enabled: true, last_sent_at: t('10:00:00') })).toEqual({ label: 'Delivered', tone: 'ok' });
    expect(deliveryState({ enabled: true, last_sent_at: t('09:00:00'), last_error: 'telegram: 403', last_error_at: t('10:00:00') })).toEqual({
      label: 'Last delivery failed',
      tone: 'warn',
      detail: 'telegram: 403',
    });
  });
  it('a success newer than the recorded failure wins', () => {
    expect(deliveryState({ enabled: true, last_sent_at: t('11:00:00'), last_error: 'old', last_error_at: t('10:00:00') }).tone).toBe('ok');
  });
});
