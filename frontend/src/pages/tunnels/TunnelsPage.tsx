import { useState } from 'react';
import { FormProvider, useWatch } from 'react-hook-form';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  Alert,
  Button,
  Card,
  Col,
  ConfigProvider,
  Descriptions,
  Form,
  Input,
  Layout,
  Modal,
  Radio,
  Row,
  Select,
  Space,
  Spin,
  Tag,
  Typography,
  message,
} from 'antd';
import { ReloadOutlined, PlusOutlined, LinkOutlined } from '@ant-design/icons';
import { HttpUtil } from '@/utils';
import { useTheme } from '@/hooks/useTheme';
import { useNodesQuery } from '@/api/queries/useNodesQuery';
import AppSidebar from '@/layouts/AppSidebar';
import { FormField, useZodForm } from '@/components/form/rhf';
import {
  TunnelCreateSchema,
  TunnelListSchema,
  validCallURL,
  type TunnelCreate,
  type TunnelView,
} from '@/schemas/call-tunnel';

const key = ['call-tunnels'];
const options = { headers: { 'Content-Type': 'application/json' }, silent: true, timeout: 130000 };
async function post(path: string, data: unknown) {
  const msg = await HttpUtil.post(`/panel/api/tunnels/${path}`, data, options);
  if (!msg.success) throw new Error(msg.msg || 'Tunnel operation failed');
}
export default function TunnelsPage() {
  const { t } = useTranslation();
  const { antdThemeConfig, isDark, isUltra } = useTheme();
  const { nodes } = useNodesQuery();
  const cache = useQueryClient();
  const [messages, messageContext] = message.useMessage();
  const [modal, modalContext] = Modal.useModal();
  const [creating, setCreating] = useState(false);
  const [roomTarget, setRoomTarget] = useState<TunnelView | null>(null);
  const [room, setRoom] = useState('');
  const methods = useZodForm<TunnelCreate>(TunnelCreateSchema, {
    defaultValues: {
      instanceId: '',
      provider: 'vk',
      nodeId: 0,
      outboundTag: 'vk-tunnel',
      adopt: false,
      room: '',
    },
  });
  const adopt = useWatch({ control: methods.control, name: 'adopt' });
  const query = useQuery({
    queryKey: key,
    queryFn: async () => {
      const msg = await HttpUtil.get('/panel/api/tunnels/list', undefined, {
        silent: true,
        timeout: 100000,
      });
      if (!msg.success) throw new Error(msg.msg || 'Tunnel status unavailable');
      return TunnelListSchema.parse(msg.obj);
    },
    refetchInterval: 30000,
  });
  const mutation = useMutation({
    mutationFn: ({ path, data }: { path: string; data: unknown }) => post(path, data),
    onSuccess: () => {
      messages.success(t('pages.tunnels.done'));
      setCreating(false);
      setRoomTarget(null);
    },
    onError: (e: Error) => messages.error(e.message),
    onSettled: () => {
      void cache.invalidateQueries({ queryKey: key });
    },
  });
  const create = methods.handleSubmit((data) => mutation.mutate({ path: 'create', data }));
  function openCreate(v?: TunnelView) {
    const p = v?.provider ?? 'vk';
    const suffix = (query.data ?? []).filter((item) => item.pair).length + 1;
    methods.reset({
      instanceId: v?.instanceId ?? '',
      provider: p,
      nodeId: 0,
      outboundTag: `${p}-tunnel-${suffix}`,
      adopt: !!v,
      room: v?.local.room ?? '',
    });
    setCreating(true);
  }
  function restart(v: TunnelView) {
    modal.confirm({
      title: t('pages.tunnels.restart'),
      content: t('pages.tunnels.restartWarning'),
      onOk: () =>
        mutation.mutateAsync({ path: `${v.provider}/restart`, data: { instanceId: v.instanceId } }),
    });
  }
  return (
    <ConfigProvider theme={antdThemeConfig}>
      {messageContext}
      {modalContext}
      <Layout
        className={['tunnels-page', isDark ? 'is-dark' : '', isUltra ? 'is-ultra' : ''].join(' ')}
      >
        <AppSidebar />
        <Layout className="content-shell">
          <Layout.Content className="content-area">
            <Space orientation="vertical" size="large" style={{ width: '100%' }}>
              <Card>
                <Space wrap>
                  <Typography.Title level={3} style={{ margin: 0 }}>
                    {t('menu.tunnels')}
                  </Typography.Title>
                  <Button
                    icon={<ReloadOutlined />}
                    loading={query.isFetching}
                    onClick={() => void query.refetch()}
                  >
                    {t('refresh')}
                  </Button>
                  <Button
                    type="primary"
                    icon={<PlusOutlined />}
                    disabled={mutation.isPending}
                    onClick={() => openCreate()}
                  >
                    {t('pages.tunnels.add')}
                  </Button>
                </Space>
                <Typography.Paragraph style={{ marginTop: 12, marginBottom: 0 }}>
                  {t('pages.tunnels.intro')}
                </Typography.Paragraph>
              </Card>
              {query.error && <Alert type="error" showIcon title={query.error.message} />}
              <Spin spinning={query.isPending}>
                <Row gutter={[16, 16]}>
                  {(query.data ?? []).map((v) => (
                    <Col xs={24} xl={12} key={`${v.provider}:${v.instanceId ?? ''}`}>
                      <Card
                        title={`${v.provider === 'vk' ? 'VK' : 'Телемост'} · ${v.pair?.outboundTag ?? (v.instanceId || 'legacy')}`}
                        extra={
                          <Tag color={v.local.state === 'active' ? 'green' : 'default'}>
                            {v.local.state}
                          </Tag>
                        }
                      >
                        <Descriptions
                          column={1}
                          size="small"
                          items={[
                            {
                              key: 'instance',
                              label: t('pages.tunnels.instanceId'),
                              children: v.instanceId || 'legacy',
                            },
                            {
                              key: 'local',
                              label: t('pages.tunnels.local'),
                              children: `${v.local.role || '—'} · ${v.local.state}`,
                            },
                            {
                              key: 'peer',
                              label: t('pages.tunnels.peer'),
                              children: v.pair
                                ? `${nodes.find((n) => n.id === v.pair?.nodeId)?.name ?? v.pair.nodeId} · ${v.peer?.state ?? '—'}`
                                : '—',
                            },
                            {
                              key: 'socks',
                              label: t('pages.tunnels.socksPort'),
                              children: v.local.port || '—',
                            },
                            ...(v.provider === 'vk' && v.peer?.port
                              ? [
                                  {
                                    key: 'udp',
                                    label: t('pages.tunnels.udpPort'),
                                    children: v.peer.port,
                                  },
                                ]
                              : []),
                            {
                              key: 'out',
                              label: t('pages.tunnels.outbound'),
                              children: v.pair?.outboundTag ?? '—',
                            },
                            {
                              key: 'room',
                              label: t('pages.tunnels.room'),
                              children: (
                                <Typography.Text style={{ wordBreak: 'break-all' }}>
                                  {v.local.room || '—'}
                                </Typography.Text>
                              ),
                            },
                            {
                              key: 'ip',
                              label: t('pages.tunnels.exitIP'),
                              children: v.pair?.exitIp ?? '—',
                            },
                            {
                              key: 'check',
                              label: t('pages.tunnels.lastCheck'),
                              children: v.pair?.lastCheck
                                ? new Date(v.pair.lastCheck).toLocaleString()
                                : '—',
                            },
                          ]}
                        />
                        {(v.error || v.local.error || v.peer?.error || v.pair?.lastError) && (
                          <Alert
                            style={{ marginTop: 16 }}
                            type="warning"
                            showIcon
                            title={v.error || v.local.error || v.peer?.error || v.pair?.lastError}
                          />
                        )}
                        <Space wrap style={{ marginTop: 20 }}>
                          {!v.pair ? (
                            <Button
                              icon={<LinkOutlined />}
                              onClick={() => openCreate(v)}
                              disabled={mutation.isPending}
                            >
                              {t('pages.tunnels.attach')}
                            </Button>
                          ) : (
                            <>
                              <Button
                                loading={mutation.isPending}
                                onClick={() =>
                                  mutation.mutate({
                                    path: `${v.provider}/check`,
                                    data: { instanceId: v.instanceId },
                                  })
                                }
                              >
                                {t('pages.tunnels.check')}
                              </Button>
                              <Button
                                disabled={mutation.isPending}
                                onClick={() => {
                                  setRoom(v.local.room);
                                  setRoomTarget(v);
                                }}
                              >
                                {t('pages.tunnels.changeRoom')}
                              </Button>
                              <Button disabled={mutation.isPending} onClick={() => restart(v)}>
                                {t('pages.tunnels.restart')}
                              </Button>
                            </>
                          )}
                        </Space>
                      </Card>
                    </Col>
                  ))}
                </Row>
              </Spin>
            </Space>
            <Modal
              open={creating}
              title={t('pages.tunnels.add')}
              confirmLoading={mutation.isPending}
              onCancel={() => {
                if (!mutation.isPending) setCreating(false);
              }}
              onOk={() => void create()}
              okText={adopt ? t('pages.tunnels.attach') : t('pages.tunnels.install')}
            >
              <FormProvider {...methods}>
                <Form layout="vertical" component="div">
                  <FormField name="provider" label={t('pages.tunnels.provider')}>
                    <Select
                      options={[
                        { value: 'vk', label: 'VK' },
                        { value: 'telemost', label: 'Телемост' },
                      ]}
                    />
                  </FormField>
                  <FormField name="adopt" label={t('pages.tunnels.mode')}>
                    <Radio.Group
                      options={[
                        { value: true, label: t('pages.tunnels.attach') },
                        { value: false, label: t('pages.tunnels.install') },
                      ]}
                    />
                  </FormField>
                  {adopt && (
                    <FormField name="instanceId" label={t('pages.tunnels.instanceId')}>
                      <Input placeholder={t('pages.tunnels.instanceHint')} />
                    </FormField>
                  )}
                  <FormField
                    name="nodeId"
                    label={t('pages.tunnels.peer')}
                    transform={{ input: (v) => (v === 0 ? undefined : v), output: (v) => v ?? 0 }}
                  >
                    <Select
                      options={nodes
                        .filter((n) => n.enable)
                        .map((n) => ({ value: n.id, label: n.name }))}
                    />
                  </FormField>
                  <FormField name="outboundTag" label={t('pages.tunnels.outbound')}>
                    <Input />
                  </FormField>
                  {!adopt && (
                    <>
                      <FormField name="room" label={t('pages.tunnels.room')}>
                        <Input />
                      </FormField>
                    </>
                  )}
                  <Alert
                    showIcon
                    type="info"
                    title={adopt ? t('pages.tunnels.attachHint') : t('pages.tunnels.installHint')}
                  />
                </Form>
              </FormProvider>
            </Modal>
            <Modal
              open={!!roomTarget}
              title={t('pages.tunnels.changeRoom')}
              confirmLoading={mutation.isPending}
              onCancel={() => {
                if (!mutation.isPending) setRoomTarget(null);
              }}
              okButtonProps={{ disabled: !roomTarget || !validCallURL(roomTarget.provider, room) }}
              onOk={() => {
                if (roomTarget)
                  mutation.mutate({
                    path: `${roomTarget.provider}/room`,
                    data: { room, instanceId: roomTarget.instanceId },
                  });
              }}
            >
              <Typography.Paragraph>
                {roomTarget?.provider === 'telemost'
                  ? t('pages.tunnels.bothEnds')
                  : t('pages.tunnels.clientOnly')}
              </Typography.Paragraph>
              <Input
                aria-label={t('pages.tunnels.room')}
                value={room}
                onChange={(e) => setRoom(e.target.value)}
              />
            </Modal>
          </Layout.Content>
        </Layout>
      </Layout>
    </ConfigProvider>
  );
}
