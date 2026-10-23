// Adapted from @json-render/shadcn 0.21.1 (Apache-2.0); see NOTICE and LICENSE.
import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
