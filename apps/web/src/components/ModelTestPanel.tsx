import type { FormEvent } from 'react';
import { copy } from '../copy';
import type { Channel, ModelTestProtocol, ModelTestResult } from '../types';
import { Icon } from './Icon';
import './ModelTestPanel.css';

const PROTOCOLS: ReadonlyArray<ModelTestProtocol> = ['chat', 'responses', 'messages'];

type ModelTestPanelProps = {
  channels: Channel[];
  channel: Channel | null;
  model: string;
  protocol: ModelTestProtocol;
  prompt: string;
  sending: boolean;
  result: ModelTestResult | null;
  catalog: string[];
  discovering: boolean;
  catalogError: string;
  onChannelChange: (id: number) => void;
  onModelChange: (value: string) => void;
  onProtocolChange: (value: ModelTestProtocol) => void;
  onPromptChange: (value: string) => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
};

const protocolLabel = (protocol: ModelTestProtocol, channel: Channel | null): string => {
  if (protocol === 'chat') return 'chat / completions';
  if (protocol === 'responses') {
    return channel && !channel.supports_responses
      ? 'responses（该渠道未勾选此能力）'
      : 'responses';
  }
  if (channel && !channel.messages_base_url) return 'messages（未配置 Messages 地址）';
  return channel && !channel.supports_messages
    ? 'messages（该渠道未勾选此能力）'
    : 'messages';
};

export function ModelTestPanel({
  channels,
  channel,
  model,
  protocol,
  prompt,
  sending,
  result,
  catalog,
  discovering,
  catalogError,
  onChannelChange,
  onModelChange,
  onProtocolChange,
  onPromptChange,
  onSubmit,
}: ModelTestPanelProps) {
  const modelOptions = channel
    ? [...new Set([channel.remote_model, ...catalog, ...(model ? [model] : [])])]
    : [];
  const mapped = channel !== null && model === channel.remote_model;
  const nonJSON = result !== null && typeof result.body === 'string';
  const renderedBody = result
    ? nonJSON
      ? String(result.body)
      : JSON.stringify(result.body, null, 2) ?? ''
    : '';

  return (
    <>
      <div className="notice">
        <Icon name="info" />
        <span>{copy.modelTestNotice}</span>
      </div>
      <section className="search-panel">
        <form onSubmit={onSubmit}>
          <div className="field-grid">
            <label className="field" htmlFor="model-test-channel">
              渠道
              <select
                id="model-test-channel"
                value={channel?.id ?? ''}
                onChange={(event) => onChannelChange(Number(event.target.value))}
              >
                <option value="" disabled>
                  选择渠道
                </option>
                {channels.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.name}（{item.enabled ? '已启用' : '已停用'}）
                  </option>
                ))}
              </select>
            </label>
            <label className="field" htmlFor="model-test-protocol">
              协议
              <select
                id="model-test-protocol"
                value={protocol}
                onChange={(event) => onProtocolChange(event.target.value as ModelTestProtocol)}
              >
                {PROTOCOLS.map((item) => (
                  <option key={item} value={item}>
                    {protocolLabel(item, channel)}
                  </option>
                ))}
              </select>
            </label>
          </div>

          <label className="field" htmlFor="model-test-model">
            模型
            <input
              id="model-test-model"
              list="model-test-catalog"
              value={model}
              disabled={!channel}
              onChange={(event) => onModelChange(event.target.value)}
              placeholder="选择上游模型，或直接输入模型名"
            />
            <datalist id="model-test-catalog">
              {modelOptions.map((item) => (
                <option key={item} value={item} />
              ))}
            </datalist>
            {channel && (
              <small>{mapped ? copy.modelTestMapped : copy.modelTestUnmapped}</small>
            )}
            {discovering && <small>正在查询上游模型目录…</small>}
            {catalogError && (
              <small className="model-test-catalog-error">
                {catalogError}；{copy.modelTestCatalogFailed}
              </small>
            )}
          </label>

          <label className="field" htmlFor="model-test-prompt">
            提示词
            <textarea
              id="model-test-prompt"
              required
              rows={5}
              value={prompt}
              onChange={(event) => onPromptChange(event.target.value)}
              placeholder="输入要发送给模型的提示词"
            />
          </label>

          <div>
            <button type="submit" className="button button--primary" disabled={sending || !channel}>
              <Icon name="test" />
              {sending ? '请求中…' : '发送测试'}
            </button>
          </div>
        </form>

        {result && (
          <>
            <div className="model-test-meta">
              <span>
                {result.protocol} · {result.channel} ·{' '}
                <span className="mono">{result.model}</span> · HTTP {result.status} ·{' '}
                {result.duration_ms} ms
              </span>
              {result.truncated && (
                <span className="model-test-badge">{copy.modelTestTruncated}</span>
              )}
              {nonJSON && <span className="model-test-badge">{copy.modelTestNotJSON}</span>}
            </div>
            <pre className="search-result">{renderedBody}</pre>
          </>
        )}
      </section>
    </>
  );
}
