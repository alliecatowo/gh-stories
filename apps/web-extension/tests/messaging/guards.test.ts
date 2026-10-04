import { describe, expect, it } from 'vitest';
import { isRuntimeRequest } from '../../src/messaging/guards.js';

const UUID = '3f0f7a5e-2a43-4b4e-9d57-0c6a1d2f8e11';

describe('message guards: identifier formats', () => {
  it('accepts well-formed logins and UUID ids', () => {
    expect(isRuntimeRequest({ type: 'ghs:stories/user', login: 'octocat' })).toBe(true);
    expect(isRuntimeRequest({ type: 'ghs:stories/user', login: 'a-b-c' })).toBe(true);
    expect(isRuntimeRequest({ type: 'ghs:story/get', storyId: UUID })).toBe(true);
    expect(isRuntimeRequest({ type: 'ghs:media/fetch', storyId: UUID, variant: 'thumb' })).toBe(true);
  });

  it.each(['..', '../stories', 'a/b', 'a?x=1', 'a#b', '-lead', 'x'.repeat(40), 'sp ace', ''])(
    'rejects login %j in every login-carrying message',
    (login) => {
      expect(isRuntimeRequest({ type: 'ghs:stories/user', login })).toBe(false);
      expect(isRuntimeRequest({ type: 'ghs:graph/action', action: 'follow', login })).toBe(false);
      expect(isRuntimeRequest({ type: 'ghs:ring/status', githubUserIds: [], logins: [login] })).toBe(false);
    },
  );

  it.each(['..', '../me', 'not-a-uuid', '', '123'])('rejects id %j', (id) => {
    expect(isRuntimeRequest({ type: 'ghs:story/get', storyId: id })).toBe(false);
    expect(isRuntimeRequest({ type: 'ghs:story/delete', storyId: id })).toBe(false);
    expect(isRuntimeRequest({ type: 'ghs:story/reply', storyId: id, body: 'hi' })).toBe(false);
    expect(isRuntimeRequest({ type: 'ghs:settings/revoke-session', sessionId: id })).toBe(false);
    expect(isRuntimeRequest({ type: 'ghs:audience/delete', listId: id })).toBe(false);
    expect(isRuntimeRequest({ type: 'ghs:media/fetch', storyId: id, variant: 'image' })).toBe(false);
  });

  it('rejects unknown media variants and path-like variants', () => {
    expect(isRuntimeRequest({ type: 'ghs:media/fetch', storyId: UUID, variant: '../x' })).toBe(false);
    expect(isRuntimeRequest({ type: 'ghs:media/fetch', storyId: UUID, variant: 'huge' })).toBe(false);
  });

  it('narrows reactions to the supported emoji', () => {
    expect(isRuntimeRequest({ type: 'ghs:story/react', storyId: UUID, emoji: '🔥' })).toBe(true);
    expect(isRuntimeRequest({ type: 'ghs:story/react', storyId: UUID, emoji: null })).toBe(true);
    expect(isRuntimeRequest({ type: 'ghs:story/react', storyId: UUID, emoji: 'not an emoji' })).toBe(false);
  });

  it('keeps the 100-per-request ring limit', () => {
    const logins = Array.from({ length: 101 }, (_, i) => `user${i}`);
    expect(isRuntimeRequest({ type: 'ghs:ring/status', githubUserIds: [], logins })).toBe(false);
    expect(isRuntimeRequest({ type: 'ghs:ring/status', githubUserIds: [], logins: logins.slice(0, 100) })).toBe(true);
  });
});
