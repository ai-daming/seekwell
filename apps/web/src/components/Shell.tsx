import type { ReactNode } from 'react';
import { copy } from '../copy';
import type { Tab } from '../types';
import { Icon, type IconName } from './Icon';
import { ThemeControl } from './ThemeControl';

const NAV: ReadonlyArray<{ id: Tab; icon: IconName; label: string }> = [
  { id: 'overview', icon: 'overview', label: '总览' },
  { id: 'channels', icon: 'channels', label: '渠道管理' },
  { id: 'keys', icon: 'stats', label: '访问 Key' },
  { id: 'usage', icon: 'stats', label: '使用统计' },
  { id: 'test', icon: 'test', label: '模型测试' },
  { id: 'search', icon: 'search', label: '搜索测试' },
  { id: 'activity', icon: 'activity', label: '调用记录' },
];

const tabLabel = (tab: Tab): string =>
  NAV.find((item) => item.id === tab)?.label ?? '总览';

type ShellProps = {
  tab: Tab;
  refreshing: boolean;
  onTabChange: (tab: Tab) => void;
  onRefresh: () => void;
  children: ReactNode;
};

export function Shell({ tab, refreshing, onTabChange, onRefresh, children }: ShellProps) {
  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand">
          <span className="brand__mark">
            <Icon name="brand" />
          </span>
          <div className="brand__copy">
            <strong className="brand__name">SeekWell</strong>
            <span className="brand__caption">MODEL GATEWAY</span>
          </div>
        </div>

        <div className="side-caption">
          <span>WORKSPACE</span>
          <span>01</span>
        </div>

        <nav className="nav" aria-label="主导航">
          {NAV.map((item) => (
            <button
              key={item.id}
              type="button"
              className="nav-item"
              aria-label={item.label}
              aria-current={tab === item.id ? 'page' : undefined}
              onClick={() => onTabChange(item.id)}
            >
              <span className="nav-item__icon">
                <Icon name={item.icon} />
              </span>
              <span className="nav-item__label">{item.label}</span>
              {tab === item.id && <span className="nav-item__dot" aria-hidden="true" />}
            </button>
          ))}
        </nav>

        <div className="side-foot">
          <div className="side-foot__icon">
            <Icon name="stats" />
          </div>
          <strong>你的 DeepSeek，汇聚在一处。</strong>
          <p>连接已有渠道，让每一份额度找到用处。</p>
          <div className="version">
            <span>SEEKWELL</span>
            <span>{__SEEKWELL_VERSION__}</span>
          </div>
        </div>
      </aside>

      <div className="main">
        <header className="topbar">
          <div className="breadcrumb">
            <span>SeekWell</span>
            <span className="breadcrumb__sep">/</span>
            <span>{tabLabel(tab)}</span>
          </div>
          <div className="topbar__right">
            <span className="live-dot" aria-hidden="true" />
            <span className="live-text">本地服务</span>
            <ThemeControl />
            <button
              type="button"
              className="button button--ghost"
              onClick={onRefresh}
              disabled={refreshing}
            >
              <Icon name="refresh" />
              {refreshing ? '刷新中' : '刷新'}
            </button>
          </div>
        </header>

        <main className="main-content">
          {children}
          <footer className="footer">
            <span>SEEKWELL</span>
            <span>
              <Icon name="info" /> {copy.enableIsNotAvailable}
            </span>
          </footer>
        </main>
      </div>
    </div>
  );
}
