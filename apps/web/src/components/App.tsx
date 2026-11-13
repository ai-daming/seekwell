import { useEffect, useState } from 'react';
import type { FormEvent } from 'react';
import '../theme.css';
import '../base.css';
import './App.css';
import { api, request } from '../api';
import { copy } from '../copy';
import { freshDraft, type Channel, type ChannelUsage, type Draft, type Entry, type AccessKey, type ModelTestProtocol, type ModelTestResult, type SpeedTestResult, type Tab, type UsageStats } from '../types';
import { ActivityTable } from './ActivityTable';
import { ChannelDialog } from './ChannelDialog';
import { ChannelsTable } from './ChannelsTable';
import { Icon } from './Icon';
import { ModelsDialog } from './ModelsDialog';
import { ModelTestPanel } from './ModelTestPanel';
import { Overview } from './Overview';
import { SearchPanel } from './SearchPanel';
import { Shell } from './Shell';
import { SpeedDialog } from './SpeedDialog';
import { UsagePage } from './UsagePage';

export default function App() {
  const [keys, setKeys] = useState<AccessKey[]>([]);
  const [keyName, setKeyName] = useState('');
  const [createdKey, setCreatedKey] = useState('');
  const [channels, setChannels] = useState<Channel[]>([]);
  const [usage, setUsage] = useState<Record<number, ChannelUsage>>({});
  const [usageLoading, setUsageLoading] = useState(false);
  const [usageError, setUsageError] = useState('');
  const [stats, setStats] = useState<UsageStats | null>(null);
  const [statsLoading, setStatsLoading] = useState(false);
  const [statsError, setStatsError] = useState('');
  const [entries, setEntries] = useState<Entry[]>([]);
  const [tab, setTab] = useState<Tab>('overview');
  const [modal, setModal] = useState(false);
  const [editing, setEditing] = useState<number | null>(null);
  const [draft, setDraft] = useState<Draft>(freshDraft());
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [query, setQuery] = useState('');
  const [searching, setSearching] = useState(false);
  const [searchResult, setSearchResult] = useState('');
  const [discovered, setDiscovered] = useState<{ name: string; models: string[] } | null>(null);
  const [discovering, setDiscovering] = useState<number | null>(null);
  const [speedResult, setSpeedResult] = useState<{ name: string; result: SpeedTestResult } | null>(null);
  const [speeding, setSpeeding] = useState<number | null>(null);
  const [testChannelId, setTestChannelId] = useState<number | null>(null);
  const [testModel, setTestModel] = useState('');
  const [testProtocol, setTestProtocol] = useState<ModelTestProtocol>('chat');
  const [testPrompt, setTestPrompt] = useState('');
  const [testSending, setTestSending] = useState(false);
  const [testResult, setTestResult] = useState<ModelTestResult | null>(null);
  const [testCatalog, setTestCatalog] = useState<Record<number, string[]>>({});
  const [testDiscovering, setTestDiscovering] = useState<number | null>(null);
  const [testCatalogError, setTestCatalogError] = useState('');

  async function refreshUsage() {
    setUsageLoading(true);
    try {
      const data = await api.channelUsage();
      setUsage(Object.fromEntries(data.usage.map((item) => [item.channel_id, item])));
      setUsageError('');
    } catch (caught) {
      setUsage({});
      setUsageError(caught instanceof Error ? caught.message : '用量查询失败');
    } finally {
      setUsageLoading(false);
    }
  }

  async function refreshStats(silent = false) {
    if (!silent) setStatsLoading(true);
    try {
      const data = await api.usageStats();
      setStats(data);
      setStatsError('');
    } catch (caught) {
      setStatsError(caught instanceof Error ? caught.message : '使用统计加载失败');
    } finally {
      if (!silent) setStatsLoading(false);
    }
  }

  async function refresh(silent = false) {
    if (!silent) setLoading(true);
    try {
      const [nextChannels, nextEntries, nextKeys] = await Promise.all([api.channels(), api.requests(), request<AccessKey[]>('/api/keys')]);
      setKeys(nextKeys);
      setChannels(nextChannels);
      setEntries(nextEntries);
      setError('');
      void refreshUsage();
      void refreshStats(silent);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : '连接失败');
    } finally {
      if (!silent) setLoading(false);
    }
  }

  useEffect(() => {
    void refresh();
  }, []);

  const enabled = channels.filter((channel) => channel.enabled);
  const models = [...new Set(enabled.map((channel) => channel.model))];
  const testChannel = channels.find((channel) => channel.id === testChannelId) ?? null;

  function add() {
    setEditing(null);
    setDraft(freshDraft());
    setError('');
    setModal(true);
  }

  function edit(channel: Channel) {
    setEditing(channel.id);
    setDraft({
      ...channel,
      auth_mode: channel.kind === 'dimagent' ? 'cli_oauth' : channel.auth_mode,
      api_key: '',
    });
    setError('');
    setModal(true);
  }

  function update<K extends keyof Draft>(key: K, value: Draft[K]) {
    setDraft((current) => ({ ...current, [key]: value }));
  }

  function changeKind(kind: string) {
    const official = kind === 'deepseek';
    const openCodeGo = kind === 'opencode_go';
    const dimAgent = kind === 'dimagent';
    setDraft((current) => ({
      ...current,
      kind,
      auth_mode: dimAgent ? 'cli_oauth' : 'api_key',
      api_key: dimAgent ? '' : current.api_key,
      base_url: official
        ? 'https://api.deepseek.com/v1'
        : openCodeGo
          ? 'https://opencode.ai/inference/go/v1'
          : dimAgent
            ? 'https://dimagent.cn/v1'
            : current.base_url,
      remote_model: openCodeGo || dimAgent ? 'deepseek-v4.1-flash' : 'deepseek-flash',
      supports_tools: official || openCodeGo || dimAgent,
      supports_images: official,
      supports_responses: official,
      supports_messages: official,
      messages_base_url: official ? 'https://api.deepseek.com/anthropic/v1' : '',
    }));
  }

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSaving(true);
    setError('');
    const payload = { ...draft, api_key: draft.api_key ?? '' };
    try {
      if (editing) {
        await request<Channel>(`/api/channels/${editing}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload),
        });
      } else {
        await api.createChannel(payload);
      }
      setModal(false);
      await refresh(true);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : '保存失败');
    } finally {
      setSaving(false);
    }
  }

  async function discover(channel: Channel) {
    setDiscovering(channel.id);
    setError('');
    try {
      const data = await api.discoverModels(channel.id);
      setDiscovered({ name: channel.name, models: data.models });
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : '查询失败');
    } finally {
      setDiscovering(null);
    }
  }

  async function speedTest(channel: Channel) {
    if (channel.paid && !window.confirm(`${copy.speedTestConfirm}「${channel.name}」`)) return;
    setSpeeding(channel.id);
    setError('');
    try {
      const result = await api.speedTest(channel.id);
      setSpeedResult({ name: channel.name, result });
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : '测速失败');
    } finally {
      setSpeeding(null);
    }
  }

  async function loadTestCatalog(channel: Channel) {
    setTestDiscovering(channel.id);
    setTestCatalogError('');
    try {
      const data = await api.discoverModels(channel.id);
      setTestCatalog((current) => ({ ...current, [channel.id]: data.models }));
    } catch (caught) {
      setTestCatalogError(caught instanceof Error ? caught.message : '查询失败');
    } finally {
      setTestDiscovering(null);
    }
  }

  function selectTestChannel(id: number) {
    const channel = channels.find((item) => item.id === id) ?? null;
    setTestChannelId(channel?.id ?? null);
    setTestModel(channel?.remote_model ?? '');
    setTestCatalogError('');
    if (channel && testCatalog[channel.id] === undefined && testDiscovering !== channel.id) {
      void loadTestCatalog(channel);
    }
  }

  async function sendTest(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const channel = channels.find((item) => item.id === testChannelId);
    if (!channel) {
      setError('请先选择渠道');
      return;
    }
    if (channel.paid && !window.confirm(`${copy.modelTestConfirm}「${channel.name}」`)) return;
    setTestSending(true);
    setError('');
    try {
      const result = await api.testModel(channel.id, {
        protocol: testProtocol,
        model: testModel,
        prompt: testPrompt,
      });
      setTestResult(result);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : '测试失败');
    } finally {
      setTestSending(false);
    }
  }

  async function remove(channel: Channel) {
    if (!window.confirm(`${copy.deleteConfirm}「${channel.name}」？`)) return;
    try {
      await api.deleteChannel(channel.id);
      await refresh(true);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : '删除失败');
    }
  }

  async function toggle(channel: Channel) {
    try {
      await request<Channel>(`/api/channels/${channel.id}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ ...channel, api_key: '', enabled: !channel.enabled }),
      });
      await refresh(true);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : '更新失败');
    }
  }

  async function createKey(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); try { const result = await request<{key:string}>('/api/keys', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:keyName})}); setCreatedKey(result.key); setKeyName(''); await refresh(true); } catch (caught) { setError(caught instanceof Error ? caught.message : '创建失败'); }
  }
  async function toggleKey(key:AccessKey) { try { await request(`/api/keys/${key.id}`, {method:'PATCH',headers:{'Content-Type':'application/json'},body:JSON.stringify({enabled:!key.enabled})}); await refresh(true); } catch (caught) { setError(caught instanceof Error ? caught.message : '更新失败'); } }

  async function runSearch(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSearching(true);
    setError('');
    try {
      const result = await api.search(query);
      setSearchResult(JSON.stringify(result, null, 2));
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : '搜索失败');
    } finally {
      setSearching(false);
    }
  }

  return (
    <>
      <Shell
        tab={tab}
        refreshing={loading}
        onTabChange={setTab}
        onRefresh={() => void refresh()}
      >
        {error && !modal && (
          <div className="error" role="alert">
            {error}
          </div>
        )}

        {tab === 'overview' && (
          <Overview
            channels={channels}
            enabledCount={enabled.length}
            modelCount={models.length}
            loading={loading}
            discovering={discovering}
            speeding={speeding}
            usage={usage}
            usageLoading={usageLoading}
            usageError={usageError}
            onAdd={add}
            onManage={() => setTab('channels')}
            onDiscover={(channel) => void discover(channel)}
            onSpeedTest={(channel) => void speedTest(channel)}
            onEdit={edit}
            onToggle={(channel) => void toggle(channel)}
            onRemove={(channel) => void remove(channel)}
          />
        )}

        {tab === 'channels' && (
          <>
            <div className="page-head">
              <div>
                <div className="eyebrow">YOUR SOURCES</div>
                <h1>渠道管理</h1>
                <p>添加已有的 API 或兼容网关。数值越小，优先级越高。</p>
              </div>
              <button type="button" className="button button--primary" onClick={add}>
                <Icon name="add" /> 添加渠道
              </button>
            </div>
            <div className="notice">
              <Icon name="info" />
              <span>{copy.channelNotice}</span>
            </div>
            <section>
              {loading ? (
                <div className="empty">正在加载…</div>
              ) : (
                <ChannelsTable
                  channels={channels}
                  discovering={discovering}
                  speeding={speeding}
                  usage={usage}
                  usageLoading={usageLoading}
                  usageError={usageError}
                  onAdd={add}
                  onDiscover={(channel) => void discover(channel)}
                  onSpeedTest={(channel) => void speedTest(channel)}
                  onEdit={edit}
                  onToggle={(channel) => void toggle(channel)}
                  onRemove={(channel) => void remove(channel)}
                />
              )}
            </section>
          </>
        )}

        {tab === 'keys' && <><div className="page-head"><div><h1>访问 Key</h1><p>每位使用者独立创建，Token 仅统计上游返回的用量。</p></div></div><section><form onSubmit={(e)=>void createKey(e)}><label>使用者或用途 <input required value={keyName} onChange={e=>setKeyName(e.target.value)} /></label> <button className="button button--primary">创建 Key</button></form>{createdKey && <div><p>完整 Key 只显示一次，请立即复制保存：</p><code>{createdKey}</code> <button className="button button--ghost" onClick={()=>void navigator.clipboard.writeText(createdKey)}>复制</button> <button className="button button--ghost" onClick={()=>setCreatedKey('')}>关闭</button></div>}<div className="table-wrap"><table><thead><tr><th>名称</th><th>状态</th><th>请求</th><th>成功 / 失败</th><th>输入 / 输出 Token</th><th>用量已返回</th><th></th></tr></thead><tbody>{keys.map(k=><tr key={k.id}><td>{k.name}<br/>{k.prefix}…</td><td>{k.enabled?'已启用':'已停用'}</td><td>{k.request_count}</td><td>{k.success_count} / {k.failure_count}</td><td>{k.input_tokens ?? '未知'} / {k.output_tokens ?? '未知'}</td><td>{k.usage_known_count} / {k.request_count}</td><td><button className="button button--ghost" onClick={()=>void toggleKey(k)}>{k.enabled?'停用':'启用'}</button></td></tr>)}</tbody></table></div></section></>}

        {tab === 'usage' && (
          <UsagePage
            stats={stats}
            loading={statsLoading}
            error={statsError}
            onRetry={() => void refreshStats()}
            onGoToKeys={() => setTab('keys')}
          />
        )}

        {tab === 'test' && (
          <>
            <div className="page-head">
              <div>
                <div className="eyebrow">MODEL TEST</div>
                <h1>模型测试</h1>
                <p>选择渠道、模型与协议，发送一条真实提示词并查看上游原始回复。</p>
              </div>
            </div>
            <ModelTestPanel
              channels={channels}
              channel={testChannel}
              model={testModel}
              protocol={testProtocol}
              prompt={testPrompt}
              sending={testSending}
              result={testResult}
              catalog={testChannel ? testCatalog[testChannel.id] ?? [] : []}
              discovering={testChannel !== null && testDiscovering === testChannel.id}
              catalogError={testCatalogError}
              onChannelChange={selectTestChannel}
              onModelChange={setTestModel}
              onProtocolChange={setTestProtocol}
              onPromptChange={setTestPrompt}
              onSubmit={(event) => void sendTest(event)}
            />
          </>
        )}

        {tab === 'search' && (
          <>
            <div className="page-head">
              <div>
                <div className="eyebrow">DEEPSEEK SEARCH</div>
                <h1>搜索测试</h1>
                <p>通过已启用且允许自动调用的 DeepSeek 官方渠道发起搜索。</p>
              </div>
            </div>
            <SearchPanel
              query={query}
              searching={searching}
              result={searchResult}
              onQueryChange={setQuery}
              onSubmit={(event) => void runSearch(event)}
            />
          </>
        )}

        {tab === 'activity' && (
          <>
            <div className="page-head">
              <div>
                <div className="eyebrow">REQUEST HISTORY</div>
                <h1>调用记录</h1>
                <p>查看请求的实际渠道和响应状态。仅保留调用元数据。</p>
              </div>
              <button
                type="button"
                className="button button--ghost"
                onClick={() => void refresh()}
                disabled={loading}
              >
                <Icon name="refresh" /> 刷新
              </button>
            </div>
            <section>
              <ActivityTable entries={entries} />
            </section>
          </>
        )}
      </Shell>

      <ModelsDialog discovered={discovered} onClose={() => setDiscovered(null)} />

      <SpeedDialog measured={speedResult} onClose={() => setSpeedResult(null)} />

      <ChannelDialog
        open={modal}
        editing={editing !== null}
        draft={draft}
        error={error}
        saving={saving}
        onChange={update}
        onKindChange={changeKind}
        onClose={() => setModal(false)}
        onSubmit={(event) => void save(event)}
      />
    </>
  );
}
