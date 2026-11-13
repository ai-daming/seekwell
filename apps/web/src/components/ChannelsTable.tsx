import { copy } from '../copy';
import { kindLabel, type Channel, type ChannelUsage, type UsageWindow } from '../types';
import { Icon } from './Icon';
import './Channels.css';

type ChannelsTableProps = {
  channels: Channel[];
  compact?: boolean;
  discovering: number | null;
  speeding: number | null;
  usage?: Record<number, ChannelUsage>;
  usageLoading?: boolean;
  usageError?: string;
  onAdd: () => void;
  onDiscover: (channel: Channel) => void;
  onSpeedTest: (channel: Channel) => void;
  onEdit: (channel: Channel) => void;
  onToggle: (channel: Channel) => void;
  onRemove: (channel: Channel) => void;
};

function formatNumber(value: number): string {
  return value.toLocaleString('zh-CN', { maximumFractionDigits: 2 });
}

function formatDate(value?: string): string {
  if (!value) return '';
  try {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return '';
    return date.toLocaleString('zh-CN');
  } catch {
    return '';
  }
}

function truncateMessage(value?: string): string {
  const text = (value ?? '').trim();
  if (text.length <= 140) return text;
  return `${text.slice(0, 140)}…`;
}

function windowLabel(window?: string): string {
  if (window === 'five_hour') return copy.usageFiveHour;
  if (window === 'weekly') return copy.usageWeekly;
  if (window === 'monthly') return copy.usageMonthly;
  return copy.usageUnknown;
}

function nextReset(windows: UsageWindow[]): string | undefined {
  const now = Date.now();
  let best = Number.POSITIVE_INFINITY;
  let result: string | undefined;
  for (const window of windows) {
    if (!window.resets_at) continue;
    try {
      const time = new Date(window.resets_at).getTime();
      if (Number.isFinite(time) && time > now && time < best) {
        best = time;
        result = window.resets_at;
      }
    } catch {
      // Ignore an invalid reset timestamp and keep the other windows.
    }
  }
  return result;
}

function OpenCodeUsage({ usage }: { usage: ChannelUsage }) {
  const windows = usage.windows ?? [];
  const reset = nextReset(windows);
  if (windows.length === 0) {
    return <span className="cell-value">{copy.usageUnknown}</span>;
  }
  return (
    <div className="usage-lines">
      {windows.map((window, index) => (
        <span className="usage-line" key={`${window.window ?? 'window'}-${index}`}>
          {windowLabel(window.window)}{' '}
          {window.used_percent === undefined || window.used_percent === null
            ? copy.usageUnknown
            : `${formatNumber(window.used_percent)}%`}
        </span>
      ))}
      {reset && <small className="cell-sub">{copy.usageReset} {formatDate(reset)}</small>}
    </div>
  );
}

function DimAgentUsage({ usage }: { usage: ChannelUsage }) {
  const credits = usage.credits;
  if (!credits) {
    return <span className="cell-value">{copy.usageUnknown}</span>;
  }
  const unlimited = credits.unlimited === true;
  const remaining = credits.remaining;
  const total = credits.total;
  const used = credits.used;
  const usedPercent =
    !unlimited && total !== undefined && total > 0 && used !== undefined ? (used / total) * 100 : undefined;

  let primary = `${copy.usageCredits} ${copy.usageUnknown}`;
  if (unlimited) {
    primary = `${copy.usageCredits} ${copy.usageUnlimited}`;
  } else if (remaining !== undefined) {
    primary = `${copy.usageCredits} 剩余 ${formatNumber(remaining)}${total !== undefined ? ` / ${formatNumber(total)}` : ''}`;
  } else if (total !== undefined) {
    primary = `${copy.usageCredits} 总额 ${formatNumber(total)}`;
  }

  const windows = usage.windows ?? [];
  return (
    <div className="usage-lines">
      <span className="usage-line">{primary}</span>
      {(used !== undefined || usedPercent !== undefined) && (
        <small className="cell-sub">
          {used !== undefined ? `${copy.usageUsed} ${formatNumber(used)}` : ''}
          {usedPercent !== undefined ? `${used !== undefined ? ' · ' : ''}${formatNumber(usedPercent)}%` : ''}
        </small>
      )}
      {credits.expires_at && (
        <small className="cell-sub">{copy.usageExpires} {formatDate(credits.expires_at)}</small>
      )}
      {windows.map((window, index) => {
        const hours = window.duration_hours !== undefined ? `${formatNumber(window.duration_hours)} 小时` : copy.usageUnknown;
        const usedTokens = window.token_used !== undefined ? formatNumber(window.token_used) : copy.usageUnknown;
        const capTokens = window.token_cap !== undefined ? formatNumber(window.token_cap) : copy.usageUnknown;
        const reset = formatDate(window.resets_at);
        return (
          <small className="cell-sub" key={`dim-window-${index}`}>
            {hours} {usedTokens} / {capTokens} {copy.usageTokenUnit}
            {reset ? ` · ${copy.usageReset} ${reset}` : ''}
          </small>
        );
      })}
    </div>
  );
}

function UsageCell({
  channel,
  usage,
  loading,
  error,
}: {
  channel: Channel;
  usage?: ChannelUsage;
  loading: boolean;
  error: string;
}) {
  const queryable = channel.kind === 'opencode_go' || channel.kind === 'dimagent';
  if (!usage) {
    if (queryable && loading) {
      return <span className="cell-value">{copy.usageLoading}</span>;
    }
    if (queryable && error) {
      return (
        <div className="usage-lines">
          <span className="cell-value">{copy.usageUnavailable}</span>
          <small className="cell-sub usage-cell__message">{truncateMessage(error)}</small>
        </div>
      );
    }
    return (
      <div className="usage-lines">
        <span className="cell-value">—</span>
        <small className="cell-sub">{copy.usageUnsupported}</small>
      </div>
    );
  }
  if (usage.status === 'unsupported') {
    return (
      <div className="usage-lines">
        <span className="cell-value">—</span>
        <small className="cell-sub">{copy.usageCustomUnsupported}</small>
      </div>
    );
  }
  if (usage.status === 'error') {
    return (
      <div className="usage-lines">
        <span className="cell-value">{copy.usageUnavailable}</span>
        <small className="cell-sub usage-cell__message">{truncateMessage(usage.message) || copy.usageUnknown}</small>
      </div>
    );
  }
  if (usage.kind === 'opencode_go') {
    return <OpenCodeUsage usage={usage} />;
  }
  if (usage.kind === 'dimagent') {
    return <DimAgentUsage usage={usage} />;
  }
  return (
    <div className="usage-lines">
      <span className="cell-value">—</span>
      <small className="cell-sub">{copy.usageUnsupported}</small>
    </div>
  );
}

export function ChannelsTable({
  channels,
  compact = false,
  discovering,
  speeding,
  usage = {},
  usageLoading = false,
  usageError = '',
  onAdd,
  onDiscover,
  onSpeedTest,
  onEdit,
  onToggle,
  onRemove,
}: ChannelsTableProps) {
  if (channels.length === 0) {
    return (
      <div className="empty">
        <div className="empty__icon">
          <Icon name="empty" />
        </div>
        <strong>{copy.emptyChannels}</strong>
        <p>添加 DeepSeek 官方 API，或连接已有的兼容网关。</p>
        <button type="button" className="button button--primary" onClick={onAdd}>
          <Icon name="add" /> 添加渠道
        </button>
      </div>
    );
  }

  const rows = compact ? channels.slice(0, 5) : channels;

  return (
    <div className="table-scroll">
      <table>
        <caption className="sr-only">渠道列表</caption>
        <thead>
          <tr>
            <th>渠道 / 来源</th>
            <th>统一模型</th>
            <th>计费方式</th>
            <th>用量 / 额度</th>
            <th>状态</th>
            <th>优先级</th>
            <th aria-label="操作" />
          </tr>
        </thead>
        <tbody>
          {rows.map((channel) => (
            <tr key={channel.id}>
              <td>
                <div className="channel-cell">
                  <span className="channel-mark" aria-hidden="true">
                    {channel.name.slice(0, 1).toUpperCase()}
                  </span>
                  <span className="channel-cell__text">
                    <b className="channel-cell__name">{channel.name}</b>
                    <small className="cell-sub">{kindLabel(channel.kind)}</small>
                  </span>
                </div>
              </td>
              <td>
                <span className="cell-value mono">{channel.model}</span>
                <small className="cell-sub mono">上游 {channel.remote_model}</small>
              </td>
              <td>
                <span className="cell-value">
                  {channel.paid ? '按量 / 付费' : '免费 / 已有套餐'}
                </span>
                <small className="cell-sub">
                  {channel.paid
                    ? channel.auto_paid
                      ? '允许自动使用'
                      : '自动使用已关闭'
                    : '以渠道实际规则为准'}
                </small>
              </td>
              <td className="usage-cell" aria-label={copy.usageTitle}>
                <UsageCell
                  channel={channel}
                  usage={usage[channel.id]}
                  loading={usageLoading}
                  error={usageError}
                />
              </td>
              <td>
                <span className={`status ${channel.enabled ? '' : 'status--off'}`}>
                  <i className="status__dot" aria-hidden="true" />
                  {channel.enabled ? '已启用' : '已停用'}
                </span>
                <small className="cell-sub">{copy.unverifiedAvailability}</small>
                {channel.auth_state && channel.auth_state !== 'ok' && <small className="cell-sub error">{channel.auth_message}</small>}
              </td>
              <td className="priority">{String(channel.priority).padStart(2, '0')}</td>
              <td>
                <div className="actions">
                  <button
                    type="button"
                    className="button button--ghost"
                    disabled={discovering === channel.id}
                    onClick={() => onDiscover(channel)}
                  >
                    <Icon name="catalog" />
                    {discovering === channel.id ? '查询中' : '查询模型'}
                  </button>
                  <button
                    type="button"
                    className="button button--ghost"
                    disabled={speeding === channel.id}
                    onClick={() => onSpeedTest(channel)}
                  >
                    <Icon name="speed" />
                    {speeding === channel.id ? '测速中…' : '测速'}
                  </button>
                  <button
                    type="button"
                    className="button button--ghost"
                    onClick={() => onEdit(channel)}
                  >
                    <Icon name="edit" /> 编辑
                  </button>
                  <button
                    type="button"
                    className="button button--ghost"
                    onClick={() => onToggle(channel)}
                  >
                    <Icon name={channel.enabled ? 'pause' : 'play'} />
                    {channel.enabled ? '停用' : '启用'}
                  </button>
                  {!compact && (
                    <button
                      type="button"
                      className="button button--ghost button--danger"
                      onClick={() => onRemove(channel)}
                    >
                      <Icon name="delete" /> 删除
                    </button>
                  )}
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
