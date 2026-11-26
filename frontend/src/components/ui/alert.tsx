import React from 'react';
import { cn } from '../../lib/utils';

interface AlertProps extends React.HTMLAttributes<HTMLDivElement> {
  variant?: 'default' | 'destructive';
}

type AlertDescriptionProps = React.HTMLAttributes<HTMLDivElement>

const Alert = React.forwardRef<HTMLDivElement, AlertProps>(
  ({ className, variant = 'default', ...props }, ref) => {
    return (
      <div
        ref={ref}
        role="alert"
        className={cn(
          'relative w-full rounded-lg border p-4 [&>svg~*]:pl-7 [&>svg+div]:translate-y-[-3px] [&>svg]:absolute [&>svg]:left-4 [&>svg]:top-4 [&>svg]:text-foreground',
          {
            'border-destructive/40 text-destructive': variant === 'destructive',
            'border-info/40 text-info': variant === 'default',
          },
          className
        )}
        {...props}
      />
    );
  }
);
Alert.displayName = 'Alert';

const AlertDescription = React.forwardRef<HTMLDivElement, AlertDescriptionProps>(
  ({ className, ...props }, ref) => {
    return (
      <div
        ref={ref}
        className={cn('text-sm [&_p]:leading-relaxed', className)}
        {...props}
      />
    );
  }
);
AlertDescription.displayName = 'AlertDescription';

export { Alert, AlertDescription };
