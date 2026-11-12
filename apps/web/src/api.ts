import type { Channel, Draft, Entry, ModelTestInput, ModelTestResult, SpeedTestResult } from './types';

export async function request<T>(path: string, opts?: RequestInit): Promise<T> {
  const response = await fetch(path, opts);
  if (!response.ok) {
    let message = `请求失败 (${response.status})`;
    try {
      const body = (await response.json()) as { error?: { message?: string } };
      message = body.error?.message || message;
    } catch {
      // Keep the status-based message when the response is not JSON.
    }
    throw new Error(message);
  }
  if (response.status === 204) return undefined as T;
  return response.json();
}

const jsonHeaders = { 'Content-Type': 'application/json' };

export const api = {
  channels: () => request<Channel[]>('/api/channels'),
  requests: () => request<Entry[]>('/api/requests'),
  createChannel: (draft: Draft) =>
    request<Channel>('/api/channels', {
      method: 'POST',
      headers: jsonHeaders,
      body: JSON.stringify(draft),
    }),
  updateChannel: (id: number, channel: Channel | Draft) =>
    request<Channel>(`/api/channels/${id}`, {
      method: 'PUT',
      headers: jsonHeaders,
      body: JSON.stringify(channel),
    }),
  deleteChannel: (id: number) =>
    request<void>(`/api/channels/${id}`, { method: 'DELETE' }),
  discoverModels: (id: number) =>
    request<{ models: string[] }>(`/api/channels/${id}/discover`, { method: 'POST' }),
  speedTest: (id: number) =>
    request<SpeedTestResult>(`/api/channels/${id}/speedtest`, { method: 'POST' }),
  testModel: (id: number, input: ModelTestInput) =>
    request<ModelTestResult>(`/api/channels/${id}/test`, {
      method: 'POST',
      headers: jsonHeaders,
      body: JSON.stringify(input),
    }),
  search: (query: string) =>
    request<unknown>('/v1/tools/search', {
      method: 'POST',
      headers: jsonHeaders,
      body: JSON.stringify({ query, count: 5 }),
    }),
};
