import type { FormEvent } from 'react';
import { copy } from '../copy';
import type { Draft } from '../types';
import { Icon } from './Icon';
import { useDialogFocus } from './useDialogFocus';

type ChannelDialogProps = {
  open: boolean;
  editing: boolean;
  draft: Draft;
  error: string;
  saving: boolean;
  onChange: <K extends keyof Draft>(key: K, value: Draft[K]) => void;
  onKindChange: (kind: string) => void;
  onClose: () => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
};

export function ChannelDialog({
  open,
  editing,
  draft,
  error,
  saving,
  onChange,
  onKindChange,
  onClose,
  onSubmit,
}: ChannelDialogProps) {
  const panelRef = useDialogFocus(open, onClose);

  if (!open) return null;

  return (
    <div
      className="overlay"
      role="presentation"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div
        ref={panelRef}
        className="dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="channel-dialog-title"
      >
        <div className="dialog__head">
          <div>
            <div className="eyebrow">SOURCE CONFIGURATION</div>
            <h2 id="channel-dialog-title">{editing ? '编辑渠道' : '添加渠道'}</h2>
            <p>一个渠道配置对应一个上游账号和模型映射。</p>
          </div>
          <button type="button" className="button button--ghost" onClick={onClose}>
            <Icon name="close" /> 关闭
          </button>
        </div>

        <form onSubmit={onSubmit}>
          {error && <div className="error">{error}</div>}

          <label className="field">
            渠道类型
            <select value={draft.kind} onChange={(event) => onKindChange(event.target.value)}>
              <option value="deepseek">DeepSeek 官方（支持 Search / Fetch）</option>
              <option value="opencode_go">OpenCode Go</option>
              <option value="dimagent">DimAgent</option>
              <option value="compatible">OpenAI 兼容 API / 已有网关</option>
            </select>
            <small>{copy.typeChangeReset}</small>
          </label>

          <label className="field">
            显示名称
            <input
              required
              value={draft.name}
              onChange={(event) => onChange('name', event.target.value)}
              placeholder="例如 DeepSeek 官方 / WorkBuddy 网关"
            />
          </label>

          <label className="field">
            API 地址
            <input
              required
              type="url"
              value={draft.base_url}
              onChange={(event) => onChange('base_url', event.target.value)}
              placeholder="https://api.deepseek.com/v1"
            />
            <small>填写包含 /v1 的 API 根地址。</small>
          </label>

          {draft.kind === 'dimagent' ? (
            <>
              <div className="field">
                {copy.authModeLabel}
                <span>{copy.authModeCli}</span>
              </div>
              <p className="form-hint">{copy.dimAuthHint}</p>
            </>
          ) : (
            <label className="field">
              API Key
              <input type="password" required={!editing} autoComplete="off"
                value={draft.api_key || ''} onChange={(event) => onChange('api_key', event.target.value)}
                placeholder={editing ? copy.keyKeptPlaceholder : '输入渠道的 API Key'} />
            </label>
          )}

          <div className="field-grid">
            <label className="field">
              统一模型名
              <input
                required
                value={draft.model}
                onChange={(event) => onChange('model', event.target.value)}
              />
            </label>
            <label className="field">
              上游模型名
              <input
                required
                value={draft.remote_model}
                onChange={(event) => onChange('remote_model', event.target.value)}
              />
            </label>
          </div>

          <label className="field">
            优先级
            <input
              type="number"
              value={draft.priority}
              onChange={(event) => onChange('priority', Number(event.target.value))}
            />
            <small>数字越小越优先；相同数字按添加顺序。</small>
          </label>

          <div className="capabilities">
            <strong>渠道支持的能力</strong>
            <small className="form-hint">
              请根据所选模型与渠道确认；不支持的请求不会发给此渠道。
            </small>
            <label className="field field--check">
              <input
                type="checkbox"
                checked={draft.supports_tools}
                onChange={(event) => onChange('supports_tools', event.target.checked)}
              />
              工具调用
            </label>
            <label className="field field--check">
              <input
                type="checkbox"
                checked={draft.supports_images}
                onChange={(event) => onChange('supports_images', event.target.checked)}
              />
              图片输入
            </label>
            <label className="field field--check">
              <input
                type="checkbox"
                checked={draft.supports_responses}
                onChange={(event) => onChange('supports_responses', event.target.checked)}
              />
              Responses 接口
            </label>
            <label className="field field--check">
              <input
                type="checkbox"
                checked={draft.supports_messages}
                onChange={(event) => onChange('supports_messages', event.target.checked)}
              />
              Anthropic Messages 接口
            </label>
            <small className="form-hint">{copy.messagesRequiredGuard}</small>
            {draft.supports_messages && (
              <label className="field">
                Messages API 地址
                <input
                  required
                  type="url"
                  value={draft.messages_base_url}
                  onChange={(event) => onChange('messages_base_url', event.target.value)}
                  placeholder="https://api.deepseek.com/anthropic/v1"
                />
                <small>填写包含 /v1 的 Messages API 根地址。</small>
              </label>
            )}
          </div>

          <label className="field field--check">
            <input
              type="checkbox"
              checked={draft.enabled}
              onChange={(event) => onChange('enabled', event.target.checked)}
            />
            启用渠道
          </label>
          <label className="field field--check">
            <input
              type="checkbox"
              checked={draft.paid}
              onChange={(event) => onChange('paid', event.target.checked)}
            />
            此渠道可能产生额外费用
          </label>
          <small className="form-hint">{copy.paidAutoGuard}</small>
          {draft.paid && (
            <label className="field field--check">
              <input
                type="checkbox"
                checked={draft.auto_paid}
                onChange={(event) => onChange('auto_paid', event.target.checked)}
              />
              允许自动使用此付费渠道
            </label>
          )}

          <div className="dialog__actions">
            <small className="dialog__footnote">{copy.fullObjectUpdate}</small>
            <button type="button" className="button" onClick={onClose}>
              取消
            </button>
            <button type="submit" className="button button--primary" disabled={saving}>
              {saving ? '保存中…' : '保存渠道'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
