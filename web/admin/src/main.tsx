import React from 'react';
import ReactDOM from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import 'antd/dist/reset.css';
import './styles.css';
import App from './App';
import { MuxMailAPIError } from './api';
import { localeStorageKey, normalizeLocale } from './i18n';
import { readLocalStorage } from './storage';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      retry: (failureCount, error) => {
        if (error instanceof Error && error.name === 'AbortError') {
          return false;
        }
        if (error instanceof MuxMailAPIError && error.status >= 400 && error.status < 500) {
          return false;
        }
        return failureCount < 1;
      },
      staleTime: 15000
    }
  }
});

const locale = normalizeLocale(readLocalStorage(localeStorageKey));

ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <App initialLocale={locale} />
    </QueryClientProvider>
  </React.StrictMode>
);
