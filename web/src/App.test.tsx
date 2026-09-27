import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from './App';

const api = vi.hoisted(() => ({
  getApplicationInfo: vi.fn(),
}));

vi.mock('./api/generated/sdk.gen', () => api);

describe('App', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(cleanup);

  it('loads the application name through the generated client', async () => {
    api.getApplicationInfo.mockResolvedValue({ data: { name: 'Dex Tech Blog', dexWebUrl: 'http://127.0.0.1:8802' } });
    render(<App />);
    expect(await screen.findByRole('heading', { name: 'Dex Tech Blog' })).toBeInTheDocument();
    expect(api.getApplicationInfo).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('link', { name: 'Open Dex Web' })).toHaveAttribute('href', 'http://127.0.0.1:8802');
    expect(screen.getByText(/newsletter requests start from slack and are reviewed in dex web/i)).toBeInTheDocument();
  });

  it('omits the Dex Web link when no URL is configured', async () => {
    api.getApplicationInfo.mockResolvedValue({ data: { name: 'Dex Tech Blog' } });
    render(<App />);
    expect(await screen.findByRole('heading', { name: 'Dex Tech Blog' })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Open Dex Web' })).not.toBeInTheDocument();
  });

  it('shows a loading state until application information arrives', () => {
    api.getApplicationInfo.mockReturnValue(new Promise(() => {}));
    render(<App />);
    expect(screen.getByText(/loading application/i)).toBeInTheDocument();
    expect(screen.queryByRole('heading')).not.toBeInTheDocument();
  });

  it('reports unavailable application information', async () => {
    api.getApplicationInfo.mockResolvedValue({ error: { message: 'unavailable' } });
    render(<App />);
    expect(await screen.findByRole('alert')).toHaveTextContent(/application information is unavailable/i);
  });

  it('presents no process-management controls', async () => {
    api.getApplicationInfo.mockResolvedValue({ data: { name: 'Dex Tech Blog', dexWebUrl: 'http://127.0.0.1:8802' } });
    render(<App />);
    await screen.findByRole('heading', { name: 'Dex Tech Blog' });
    expect(screen.queryAllByRole('button')).toHaveLength(0);
    expect(screen.queryAllByRole('textbox')).toHaveLength(0);
    expect(screen.queryByRole('form')).not.toBeInTheDocument();
    expect(screen.queryByText(/approve|retry|mock controls/i)).not.toBeInTheDocument();
  });
});
