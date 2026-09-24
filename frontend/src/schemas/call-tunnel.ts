import { z } from 'zod';

export const TunnelStatusSchema = z.object({
  provider: z.string(),
  installed: z.boolean(),
  state: z.string(),
  role: z.string(),
  room: z.string(),
  port: z.number(),
  fingerprint: z.string().optional(),
  exitIp: z.string().optional(),
  error: z.string().optional(),
});
export const TunnelPairSchema = z.object({
  provider: z.string(),
  nodeId: z.number(),
  outboundTag: z.string(),
  lastCheck: z.string().optional(),
  exitIp: z.string().optional(),
  lastError: z.string().optional(),
});
export const TunnelViewSchema = z.object({
  provider: z.enum(['vk', 'telemost']),
  pair: TunnelPairSchema.optional(),
  local: TunnelStatusSchema,
  peer: TunnelStatusSchema.optional(),
  error: z.string().optional(),
});
export const TunnelListSchema = z.array(TunnelViewSchema);
export type TunnelView = z.infer<typeof TunnelViewSchema>;
export const TunnelCreateSchema = z
  .object({
    provider: z.enum(['vk', 'telemost']),
    nodeId: z.number().int().positive(),
    outboundTag: z.string().regex(/^[A-Za-z0-9_.-]{1,100}$/),
    adopt: z.boolean(),
    room: z.string(),
    address: z.string(),
    port: z.number().int().min(1).max(65535),
    serverPort: z.number().int().min(1).max(65535),
  })
  .superRefine((v, ctx) => {
    if (v.adopt) return;
    if (!validCallURL(v.provider, v.room))
      ctx.addIssue({ code: 'custom', path: ['room'], message: 'pages.tunnels.invalidRoom' });
    if (
      v.provider === 'vk' &&
      !z.ipv4().safeParse(v.address).success &&
      !z.ipv6().safeParse(v.address).success
    )
      ctx.addIssue({ code: 'custom', path: ['address'], message: 'pages.tunnels.invalidIP' });
  });
export type TunnelCreate = z.infer<typeof TunnelCreateSchema>;
export function validCallURL(provider: string, value: string) {
  try {
    const u = new URL(value);
    if (u.protocol !== 'https:' || u.username || u.password || u.search || u.hash || u.port)
      return false;
    return provider === 'vk'
      ? ['vk.ru', 'vk.com'].includes(u.hostname) &&
          /^\/call\/join\/[A-Za-z0-9_-]+$/.test(u.pathname)
      : ['telemost.360.yandex.ru', 'telemost.yandex.ru'].includes(u.hostname) &&
          /^\/j\/[0-9]+$/.test(u.pathname);
  } catch {
    return false;
  }
}
