import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

const versionText = process.env.SEEKWELL_VERSION_TEXT?.trim() || '开发版';

export default defineConfig({
  plugins: [react()],
  define: {
    __SEEKWELL_VERSION__: JSON.stringify(versionText),
  },
  server: {
    proxy: { '/api': 'http://127.0.0.1:8350', '/v1': 'http://127.0.0.1:8350' },
  },
});
