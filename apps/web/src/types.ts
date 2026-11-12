export type Channel = {
  id: number;
  name: string;
  base_url: string;
  api_key?: string;
  has_key: boolean;
  model: string;
  remote_model: string;
  enabled: boolean;
  paid: boolean;
  auto_paid: boolean;
  priority: number;
  kind: string;
  supports_tools: boolean;
  supports_images: boolean;
  supports_responses: boolean;
  supports_messages: boolean;
  messages_base_url: string;
};

export type Entry = {
  created_at: string;
  model: string;
  channel: string;
  status: number;
  duration_ms: number;
};

export type SpeedTestResult = {
  duration_ms: number;
  status: number;
  model: string;
};

export type Draft = Omit<Channel, 'id' | 'has_key'>;

export type Tab = 'overview' | 'channels' | 'search' | 'activity';

export const freshDraft = (): Draft => ({
  name: '',
  base_url: 'https://api.deepseek.com/v1',
  api_key: '',
  model: 'deepseek-flash',
  remote_model: 'deepseek-flash',
  enabled: true,
  paid: true,
  auto_paid: false,
  priority: 0,
  kind: 'deepseek',
  supports_tools: true,
  supports_images: true,
  supports_responses: true,
  supports_messages: true,
  messages_base_url: 'https://api.deepseek.com/anthropic/v1',
});

export const kindLabel = (kind: string): string => {
  if (kind === 'deepseek') return 'DeepSeek 官方';
  if (kind === 'opencode_go') return 'OpenCode Go';
  return '兼容 API / 网关';
};
