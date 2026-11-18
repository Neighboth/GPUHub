import type { ReactElement } from 'react';
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import { ToastProvider } from './components/Toast';
import Layout from './components/Layout';
import { LangProvider } from './i18n';
import { getAccessToken } from './api';
import Login from './pages/Login';
import Dashboard from './pages/Dashboard';
import Providers from './pages/Providers';
import Proxies from './pages/Proxies';
import JobDetail from './pages/JobDetail';
import Keys from './pages/Keys';
import Settings from './pages/Settings';
import Audit from './pages/Audit';

function RequireAuth({ children }: { children: ReactElement }) {
  const token = getAccessToken();
  if (!token) return <Navigate to="/login" replace />;
  return children;
}

export default function App() {
  return (
    <LangProvider>
      <ToastProvider>
        <BrowserRouter>
          <Routes>
            <Route path="/login" element={<Login />} />
            <Route
              path="/"
              element={
                <RequireAuth>
                  <Layout />
                </RequireAuth>
              }
            >
              <Route index element={<Dashboard />} />
              <Route path="providers" element={<Providers />} />
              <Route path="proxies" element={<Proxies />} />
              {/* /jobs removed: jobs are submitted via code/API (see README + examples/) — monitoring lives on the Dashboard */}
              <Route path="jobs/:id" element={<JobDetail />} />
              <Route path="keys" element={<Keys />} />
              <Route path="settings" element={<Settings />} />
              <Route path="audit" element={<Audit />} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Route>
          </Routes>
        </BrowserRouter>
      </ToastProvider>
    </LangProvider>
  );
}
