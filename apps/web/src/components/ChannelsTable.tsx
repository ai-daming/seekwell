import { copy } from '../copy';
import { kindLabel, type Channel } from '../types';
import { Icon } from './Icon';
import './Channels.css';

type ChannelsTableProps = {
  channels: Channel[];
  compact?: boolean;
  discovering: number | null;
  speeding: number | null;
  onAdd: () => void;
  onDiscover: (channel: Channel) => void;
  onSpeedTest: (channel: Channel) => void;
  onEdit: (channel: Channel) => void;
  onToggle: (channel: Channel) => void;
  onRemove: (channel: Channel) => void;
};

export function ChannelsTable({
  channels,
  compact = false,
  discovering,
  speeding,
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
              <td>
                <span className={`status ${channel.enabled ? '' : 'status--off'}`}>
                  <i className="status__dot" aria-hidden="true" />
                  {channel.enabled ? '已启用' : '已停用'}
                </span>
                <small className="cell-sub">{copy.unverifiedAvailability}</small>
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
