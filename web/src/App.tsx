import { useEffect } from 'react';
import { Outlet, RouterProvider, createHashRouter } from 'react-router-dom';
import { NotificationContainer } from '@/components/common/NotificationContainer';
import { ConfirmationModal } from '@/components/common/ConfirmationModal';
import { MainLayout } from '@/components/layout/MainLayout';
import { AppErrorScreen } from '@/components/common/PageErrorBoundary';
import { ConnectionGate } from '@/router/ConnectionGate';
import { installSheetHistory } from '@/components/ui/sheetHistory';
import { installEditedHashGuard } from '@/hooks/useUnsavedChangesGuard';
import { useThemeStore } from '@/stores';

function RootShell() {
  return (
    <>
      <NotificationContainer />
      <ConfirmationModal />
      <Outlet />
    </>
  );
}

installEditedHashGuard(window);
installSheetHistory(window);

const router = createHashRouter([
  {
    element: <RootShell />,
    errorElement: <AppErrorScreen />,
    children: [
      {
        path: '/*',
        element: (
          <ConnectionGate>
            <MainLayout />
          </ConnectionGate>
        ),
      },
    ],
  },
]);

function App() {
  const initializeTheme = useThemeStore((state) => state.initializeTheme);

  useEffect(() => {
    const cleanupTheme = initializeTheme();
    return cleanupTheme;
  }, [initializeTheme]);

  return <RouterProvider router={router} />;
}

export default App;
