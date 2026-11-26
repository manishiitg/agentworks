import React, { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { CheckCircle, Info, XCircle } from 'lucide-react';

type ToastType = 'success' | 'info' | 'error';

interface ToastProps {
  message: string;
  type: ToastType;
  duration?: number;
  onClose: () => void;
}

const TOAST_CONFIG: Record<ToastType, { icon: typeof CheckCircle; bgColor: string }> = {
  success: { icon: CheckCircle, bgColor: 'text-success' },
  info: { icon: Info, bgColor: 'text-info' },
  error: { icon: XCircle, bgColor: 'text-destructive' },
};

export const Toast: React.FC<ToastProps> = ({
  message,
  type,
  duration = 3000,
  onClose
}) => {
  const [isVisible, setIsVisible] = useState(true);

  // The parent rebuilds onClose on every render, so depending on it directly
  // would restart the dismiss timer each time and keep the toast on screen.
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  useEffect(() => {
    const timer = setTimeout(() => {
      setIsVisible(false);
      setTimeout(() => onCloseRef.current(), 300); // Allow fade out animation
    }, duration);

    return () => clearTimeout(timer);
  }, [duration]);

  if (!isVisible) return null;

  const { icon: Icon, bgColor } = TOAST_CONFIG[type];

  return (
    <div className="animate-in slide-in-from-right-full duration-300">
      <div className={`border border-border bg-surface-popover text-popover-foreground px-4 py-2 rounded-lg shadow-lg flex items-center gap-2 max-w-sm`}>
        <Icon className={`w-4 h-4 flex-shrink-0 ${bgColor}`} />
        <span className="text-sm font-medium">{message}</span>
      </div>
    </div>
  );
};

interface ToastContainerProps {
  toasts: Array<{ id: string; message: string; type: ToastType }>;
  onRemoveToast: (id: string) => void;
}

export const ToastContainer: React.FC<ToastContainerProps> = ({ toasts, onRemoveToast }) => {
  if (typeof document === 'undefined') return null;

  // The stack owns the positioning so multiple toasts sit above each other
  // instead of stacking on the same fixed coordinates. Newest lands nearest
  // the corner.
  return createPortal(
    <div className="fixed bottom-4 right-4 z-[100000] flex flex-col items-end gap-2 pointer-events-none">
      {toasts.map((toast) => (
        <Toast
          key={toast.id}
          message={toast.message}
          type={toast.type}
          onClose={() => onRemoveToast(toast.id)}
        />
      ))}
    </div>,
    document.body
  );
};
