import { describe, expect, it } from 'vitest';
import { validatePresignedUpload } from '../../src/api/presign.js';

const SERVICE = 'https://gh-stories-api.example.run.app';
const R2 = 'https://acct.r2.cloudflarestorage.com/gh-stories-media/key?X-Amz-Signature=abc';

describe('validatePresignedUpload', () => {
  it('accepts an https object-store URL and signed headers, dropping browser-managed ones', () => {
    const out = validatePresignedUpload(
      R2,
      { Host: 'acct.r2.cloudflarestorage.com', 'Content-Type': 'image/png', 'Content-Length': '12', 'x-amz-meta-a': 'b' },
      SERVICE,
    );
    expect(out.url).toBe(R2);
    expect(out.headers).toEqual({ 'Content-Type': 'image/png', 'x-amz-meta-a': 'b' });
  });

  it.each([
    ['plain http', 'http://acct.r2.cloudflarestorage.com/k'],
    ['credentials in URL', 'https://u:p@acct.r2.cloudflarestorage.com/k'],
    ['loopback', 'https://localhost/k'],
    ['IPv4 literal', 'https://10.0.0.5/k'],
    ['IPv6 literal', 'https://[::1]/k'],
    ['single-label host', 'https://intranet/k'],
    ['javascript scheme', 'javascript:alert(1)'],
    ['garbage', 'not a url'],
  ])('rejects %s', (_label, url) => {
    expect(() => validatePresignedUpload(url, {}, SERVICE)).toThrow();
  });

  it('rejects headers that are not part of a presigned PUT', () => {
    expect(() => validatePresignedUpload(R2, { Authorization: 'Bearer x' }, SERVICE)).toThrow();
    expect(() => validatePresignedUpload(R2, { Cookie: 'a=b' }, SERVICE)).toThrow();
    expect(() => validatePresignedUpload(R2, { 'Content-Type': 'a\r\nX: y' }, SERVICE)).toThrow();
  });

  it('allows the service origin itself (self-hosted/local object gateway)', () => {
    expect(() => validatePresignedUpload(`${SERVICE}/upload/abc`, {}, SERVICE)).not.toThrow();
    expect(() => validatePresignedUpload('http://localhost:8787/u', {}, 'http://localhost:8787')).not.toThrow();
  });

  it('allows a local object store only when the service itself is local', () => {
    expect(() => validatePresignedUpload('http://localhost:55900/b/k', {}, 'http://localhost:8787')).not.toThrow();
    expect(() => validatePresignedUpload('http://localhost:55900/b/k', {}, SERVICE)).toThrow();
    expect(() => validatePresignedUpload('javascript:alert(1)', {}, 'http://localhost:8787')).toThrow();
  });
});
