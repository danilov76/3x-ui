import { describe, expect, it, vi } from 'vitest';
import { render, screen, within, fireEvent, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import TunnelsPage from '@/pages/tunnels/TunnelsPage';
import { HttpUtil } from '@/utils';
vi.mock('@/layouts/AppSidebar', () => ({ default: () => null }));
vi.mock('@/hooks/useTheme', () => ({ useTheme: () => ({}) }));
vi.mock('@/api/queries/useNodesQuery', () => ({ useNodesQuery: () => ({ nodes: [] }) }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock('@/utils', () => ({ HttpUtil: { get: vi.fn(), post: vi.fn() } }));

describe('multiple tunnel cards', () => {
  it('addresses the selected instance when two cards have the same provider', async () => {
    const entries = ['first', 'second'].map((id) => ({
      provider: 'vk',
      instanceId: id,
      pair: { provider: 'vk', instanceId: id, nodeId: 1, outboundTag: id },
      local: {
        provider: 'vk',
        instanceId: id,
        installed: true,
        state: 'active',
        role: 'client',
        room: '',
        port: 19094,
      },
    }));
    vi.mocked(HttpUtil.get).mockResolvedValue({ success: true, msg: '', obj: entries });
    vi.mocked(HttpUtil.post).mockResolvedValue({ success: true, msg: '', obj: null });
    const cache = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={cache}>
        <TunnelsPage />
      </QueryClientProvider>,
    );
    const title = await screen.findByText('VK · second');
    const card = title.closest('.ant-card');
    expect(card).not.toBeNull();
    fireEvent.click(within(card as HTMLElement).getByText('pages.tunnels.check'));
    await waitFor(() =>
      expect(HttpUtil.post).toHaveBeenCalledWith(
        '/panel/api/tunnels/vk/check',
        { instanceId: 'second' },
        expect.anything(),
      ),
    );
    expect(screen.getByText('VK · first')).toBeTruthy();
    fireEvent.click(screen.getByText('pages.tunnels.add'));
    await screen.findByRole('button', { name: 'pages.tunnels.install' });
    expect(screen.queryByLabelText('pages.tunnels.serverIP')).toBeNull();
    expect(screen.queryByLabelText('pages.tunnels.socksPort')).toBeNull();
    expect(screen.queryByLabelText('pages.tunnels.udpPort')).toBeNull();

    cache.clear();
  });
});
