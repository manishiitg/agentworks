import { createContext } from 'react'

/** Whether the "└ Tip: ..." line may appear under the working line. Dashboards switch it off: their chat is a tool for the
 * people using the dashboard, not a place to teach the product. */
export const WorkingTipsContext = createContext(true)
