export type Channel = {
  id: number;
  name: string;
  base_url: string;
  api_key?: string;
  has_key: boolean;
  auth_mode: 'api_key' | 'cli_oauth';
  auth_state?: string;
  auth_message?: string;
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


export type UsageWindow = {
  window?: string;
  used_percent?: number;
  resets_at?: string;
  duration_hours?: number;
  token_used?: number;
  token_cap?: number;
};

export type UsageCredits = {
  total?: number;
  used?: number;
  remaining?: number;
  unlimited?: boolean;
  unit?: string;
  expires_at?: string;
};

export type ChannelUsage = {
  channel_id: number;
  kind: string;
  status: 'ok' | 'error' | 'unsupported' | string;
  checked_at?: string;
  message?: string;
  credits?: UsageCredits;
  windows?: UsageWindow[];
};

export type Entry = {
  created_at: string;
  model: string;
  channel: string;
  kind: string;
  status: number;
  duration_ms: number;
  input_tokens: number | null;
  output_tokens: number | null;
  cache_hit_tokens: number | null;
  cache_miss_tokens: number | null;
  cost_cny: number | null;
  price_period: 'peak' | 'offpeak' | null;
  pricing_model: string | null;
  cost_unknown_reason: 'failed' | 'no_usage' | 'unpriced' | null;
};

export type SpeedTestResult = {
  duration_ms: number;
  status: number;
  model: string;
};

export type ModelTestProtocol = 'chat' | 'responses' | 'messages';

export type ModelTestInput = {
  protocol: ModelTestProtocol;
  model: string;
  prompt: string;
};

export type ModelTestResult = {
  channel: string;
  model: string;
  protocol: ModelTestProtocol;
  status: number;
  duration_ms: number;
  body: unknown;
  truncated: boolean;
};

export type Draft = Omit<Channel, 'id' | 'has_key'>;

export type Tab = 'overview' | 'channels' | 'keys' | 'usage' | 'test' | 'search' | 'activity';

export const freshDraft = (): Draft => ({
  name: '',
  base_url: 'https://api.deepseek.com/v1',
  api_key: '',
  auth_mode: 'api_key',
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
  if (kind === 'dimagent') return 'DimAgent';
  if (kind === 'compatible') return '兼容 API / 网关';
  return '未知';
};

export type AccessKey = {id:number;name:string;prefix:string;enabled:boolean;request_count:number;success_count:number;failure_count:number;usage_known_count:number;input_tokens:number|null;output_tokens:number|null};

export type AccessUsageTotals = {
  request_count: number;
  success_count: number;
  failure_count: number;
  usage_known_count: number;
  usage_unknown_count: number;
  input_tokens: number | null;
  output_tokens: number | null;
  total_tokens: number | null;
  cost_cny: number | null;
  cost_known_count: number;
  cost_unknown_count: number;
};

export type UsageOverall = AccessUsageTotals & {
  key_count: number;
  enabled_key_count: number;
  last_request_at: string | null;
};

export type UsageKeyStats = AccessUsageTotals & {
  id: number;
  name: string;
  prefix: string;
  enabled: boolean;
  created_at: string;
  last_request_at: string | null;
};

export type UsageDaily = AccessUsageTotals & {
  date: string;
};

export type PricingRate = {
  model: string;
  hit_off: number;
  hit_peak: number;
  miss_off: number;
  miss_peak: number;
  out_off: number;
  out_peak: number;
};

export type PricingHistory = {
  version: string;
  observed_at: string;
  source: 'bundled' | 'remote' | string;
  models: PricingRate[];
};

export type UsagePricing = {
  currency: string;
  unit: string;
  source: 'bundled' | 'remote' | string;
  source_url: string;
  last_sync_at: string | null;
  last_sync_ok: boolean;
  stale: boolean;
  warning: string;
  peak_timezone: string;
  peak_windows: string[];
  holiday_years: number[];
  models: PricingRate[];
  history: PricingHistory[];
};

export type UsageStats = {
  overall: UsageOverall;
  keys: UsageKeyStats[];
  daily: UsageDaily[];
  pricing: UsagePricing;
};
