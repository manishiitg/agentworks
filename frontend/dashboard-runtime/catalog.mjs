import { shadcnComponentDefinitions } from './src/shadcn/catalog.ts'
import { z } from 'zod'

// Use the entire supplied web catalog. Descriptions here explain mechanics;
// they do not prescribe the designer's layout, palette, or sample data.
export const components = {
  ...shadcnComponentDefinitions,
  Metric: { props: z.object({ label: z.string(), value: z.union([z.string(), z.number()]), detail: z.string().nullable(), className: z.string().nullable() }), description: 'A labelled metric using supplied data.' },
  BarChart: { props: z.object({ data: z.array(z.object({ label: z.string(), value: z.number() })), label: z.string().nullable(), progress: z.number().nullable(), className: z.string().nullable() }), description: 'Bar chart of supplied labelled numeric values.' },
  LineChart: { props: z.object({ data: z.array(z.object({ label: z.string(), value: z.number() })), label: z.string().nullable(), progress: z.number().nullable(), className: z.string().nullable() }), description: 'Line chart of supplied recorded values, in their given order.' },
  AskButton: { props: z.object({ label: z.string(), target: z.string().nullable(), className: z.string().nullable() }), description: 'Opens the existing project Ask composer for shared Run users only. Hidden for Builders and preview; does not send a message.' },
  ChatPanel: { props: z.object({ id: z.string(), height: z.number(), className: z.string().nullable() }), description: 'Designer-placed native project conversation for shared Run users only. Hidden for Builders and preview; requires a positive height.' },
  FunctionButton: { props: z.object({ name: z.string(), label: z.string(), defaults: z.record(z.string(), z.unknown()).nullable(), className: z.string().nullable() }), description: 'Opens the existing form for this project published function. Only the person pressing Run executes it.' },
  MotionControls: { props: z.object({ label: z.string().nullable(), className: z.string().nullable() }), description: 'Optional controls for this dashboard shared motion clock. Pass a clock created with report.motion.create().' },
}
export function catalogDocument() {
  return {
    renderer: 'tsx', version: '1',
    components: Object.fromEntries(Object.entries(components).map(([name, d]) => [name, {
      props: z.toJSONSchema(d.props), slots: d.slots ?? [], events: d.events ?? [], description: d.description,
    }])),
  }
}
