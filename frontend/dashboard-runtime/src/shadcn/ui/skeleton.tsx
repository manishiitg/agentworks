// Adapted from @json-render/shadcn 0.21.1 (Apache-2.0); see NOTICE and LICENSE.
import { cn } from "../lib/utils";

function Skeleton({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="skeleton"
      className={cn("bg-accent animate-pulse rounded-md", className)}
      {...props}
    />
  );
}

export { Skeleton };
