import { describe, expect, it } from 'vitest';
import { TunnelCreateSchema, validCallURL } from '@/schemas/call-tunnel';
describe('call tunnel form', () => {
  it('rejects wrong provider, credentials and query strings', () => {
    expect(validCallURL('vk', 'https://vk.ru/call/join/abc')).toBe(true);
    expect(validCallURL('vk', 'https://telemost.360.yandex.ru/j/123')).toBe(false);
    expect(validCallURL('vk', 'https://u:p@vk.ru/call/join/abc')).toBe(false);
    expect(validCallURL('vk', 'https://vk.ru/call/join/abc?x=1')).toBe(false);
  });
  it('allows adoption without asking for a new room or server address', () => {
    const v = {
      provider: 'vk',
      nodeId: 2,
      outboundTag: 'koara-vk',
      adopt: true,
      room: '',
    };
    expect(TunnelCreateSchema.safeParse(v).success).toBe(true);
    expect(TunnelCreateSchema.safeParse({ ...v, adopt: false }).success).toBe(false);
    expect(
      TunnelCreateSchema.safeParse({
        ...v,
        adopt: false,
        room: 'https://vk.ru/call/join/id',
      }).success,
    ).toBe(true);
  });
});
