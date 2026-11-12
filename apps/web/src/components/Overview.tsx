import { copy } from '../copy';
import type { Channel } from '../types';
import { ChannelsTable } from './ChannelsTable';
import { Icon } from './Icon';
import './Overview.css';

type OverviewProps = {
  channels: Channel[];
  enabledCount: number;
  modelCount: number;
  loading: boolean;
  discovering: number | null;
  speeding: number | null;
  onAdd: () => void;
  onManage: () => void;
  onDiscover: (channel: Channel) => void;
  onSpeedTest: (channel: Channel) => void;
  onEdit: (channel: Channel) => void;
  onToggle: (channel: Channel) => void;
  onRemove: (channel: Channel) => void;
};

export function Overview({
  channels,
  enabledCount,
  modelCount,
  loading,
  discovering,
  speeding,
  onAdd,
  onManage,
  onDiscover,
  onSpeedTest,
  onEdit,
  onToggle,
  onRemove,
}: OverviewProps) {
  return (
    <>
      <div className="overview-hero">
        <div>
          <div className="eyebrow">YOUR DEEPSEEK, CONNECTED</div>
          <h1>
            让每个来源，<span>流向同一处。</span>
          </h1>
          <p>把分散的 DeepSeek 渠道汇聚起来。选择模型，剩下的交给 SeekWell。</p>
        </div>
        <div className="overview-hero__mark">
          <Icon name="brand" />
        </div>
      </div>

      <div className="stats">
        <div className="stat-card">
          <span className="stat-card__label">已添加渠道</span>
          <strong className="stat-card__value">{String(channels.length).padStart(2, '0')}</strong>
          <span className="stat-card__note">连接的来源</span>
        </div>
        <div className="stat-card">
          <span className="stat-card__label">已启用</span>
          <strong className="stat-card__value">{String(enabledCount).padStart(2, '0')}</strong>
          <span className="stat-card__note">可参与路由的渠道</span>
        </div>
        <div className="stat-card">
          <span className="stat-card__label">统一模型</span>
          <strong className="stat-card__value">{String(modelCount).padStart(2, '0')}</strong>
          <span className="stat-card__note">基于已启用配置</span>
        </div>
        <div className="stat-card stat-card--note">
          <span className="stat-card__label">
            <Icon name="stats" /> 连接状态
          </span>
          <strong className="stat-card__value">待验证</strong>
          <p>{copy.enableIsNotAvailable}</p>
        </div>
      </div>

      <section>
        <div className="section-head">
          <div>
            <div className="eyebrow">CONNECTED SOURCES</div>
            <h2>
              渠道概览 <span>{channels.length}</span>
            </h2>
          </div>
          <button type="button" className="button button--ghost" onClick={onManage}>
            管理全部
          </button>
        </div>
        {loading ? (
          <div className="empty">正在加载…</div>
        ) : (
          <ChannelsTable
            channels={channels}
            compact
            discovering={discovering}
            speeding={speeding}
            onAdd={onAdd}
            onDiscover={onDiscover}
            onSpeedTest={onSpeedTest}
            onEdit={onEdit}
            onToggle={onToggle}
            onRemove={onRemove}
          />
        )}
      </section>

      <div className="endpoint">
        <span className="endpoint__icon">
          <Icon name="endpoint" />
        </span>
        <div>
          <strong>统一 API 入口</strong>
          <p>配置渠道后，将客户端连接到本机地址。</p>
        </div>
        <code>{window.location.origin}/v1</code>
        <button
          type="button"
          className="button button--ghost"
          onClick={() => void navigator.clipboard.writeText(`${window.location.origin}/v1`)}
        >
          <Icon name="copy" /> 复制
        </button>
      </div>
    </>
  );
}
