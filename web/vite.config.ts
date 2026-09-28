import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

export default defineConfig(() => {
  const mockTarget = process.env.VITE_MOCK_API_TARGET;
  return {
    plugins: [react()],
    server: mockTarget ? {
      proxy: {
        '/api': { target: mockTarget },
        '/__mock__': { target: mockTarget },
      },
    } : undefined,
    test: {
      environment: 'jsdom',
      include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
      setupFiles: './src/test/setup.ts',
    },
  };
});
