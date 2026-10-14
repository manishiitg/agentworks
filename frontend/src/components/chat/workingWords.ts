// Our own plain, friendly words for "the agent is busy" (display only). The server's real status always wins over them.
export const WORKING_WORDS = [
  'Thinking', 'Pondering', 'Brewing', 'Sketching', 'Tinkering', 'Weaving', 'Gathering', 'Polishing',
  'Mulling', 'Stitching', 'Shaping', 'Exploring', 'Untangling', 'Assembling', 'Crafting', 'Sorting',
  'Piecing together', 'Connecting dots', 'Warming up', 'Digging in', 'Mapping it out', 'Fine-tuning', 'Lining things up', 'Checking in',
  'Cooking up ideas', 'Working it out', 'Putting it together', 'Taking a look', 'Getting there', 'Making progress', 'Daydreaming', 'Doodling',
  'Noodling', 'Simmering', 'Stirring', 'Kneading', 'Whittling', 'Sanding', 'Painting', 'Planting',
  'Watering', 'Sprouting', 'Blooming', 'Harvesting', 'Gardening', 'Baking', 'Toasting', 'Seasoning',
  'Tasting', 'Plating', 'Folding', 'Threading', 'Knitting', 'Braiding', 'Tuning', 'Balancing',
  'Measuring', 'Weighing', 'Comparing', 'Matching', 'Sifting', 'Panning for gold', 'Following leads', 'Connecting threads',
  'Peeking around', 'Looking closer', 'Reading carefully', 'Taking notes', 'Making a list', 'Checking twice', 'Rolling up sleeves', 'Clearing the desk',
  'Opening drawers', 'Flipping pages', 'Browsing shelves', 'Counting things', 'Lining up ducks', 'Drawing a map', 'Plotting a course', 'Finding a way',
  'Making sense of it', 'Sleeping on it', 'Chewing it over', 'Turning it over', 'Trying a few ideas', 'Almost there', 'Hang tight', 'Musing',
  'Wondering', 'Reflecting', 'Ruminating', 'Contemplating', 'Considering', 'Imagining', 'Dreaming up ideas', 'Brainstorming',
  'Sleuthing', 'Investigating', 'Scouting', 'Surveying', 'Wandering', 'Strolling', 'Meandering', 'Roaming',
  'Trekking', 'Navigating', 'Charting', 'Orienting', 'Finding my footing', 'Getting my bearings', 'Settling in', 'Stretching',
  'Limbering up', 'Loosening up', 'Taking a breath', 'Taking a moment', 'Taking it slow', 'Easing in', 'Pacing myself', 'Steadying',
  'Centering', 'Focusing', 'Zooming in', 'Zooming out', 'Squinting', 'Peering', 'Glancing', 'Scanning',
  'Skimming', 'Leafing through', 'Thumbing through', 'Paging through', 'Combing through', 'Rummaging', 'Foraging', 'Gleaning',
  'Collecting thoughts', 'Gathering threads', 'Untying knots', 'Smoothing things out', 'Ironing out wrinkles', 'Tidying up', 'Dusting off ideas', 'Polishing the details',
  'Buffing', 'Trimming', 'Pruning', 'Shaping up', 'Molding', 'Sculpting', 'Carving', 'Chiseling',
  'Forging ahead', 'Hammering it out', 'Gluing pieces', 'Bolting things together', 'Wiring things up', 'Tuning in', 'Dialing in', 'Calibrating',
  'Adjusting', 'Aligning', 'Straightening', 'Leveling', 'Stacking', 'Arranging', 'Organizing', 'Filing',
  'Labeling', 'Cataloging', 'Shelving', 'Bundling', 'Packing', 'Wrapping up details', 'Tying ribbons', 'Stirring the pot',
  'Letting it simmer', 'Letting it rise', 'Letting it steep', 'Steeping', 'Marinating', 'Percolating', 'Ripening', 'Germinating',
  'Taking root', 'Unfolding', 'Unwrapping', 'Unraveling', 'Decoding', 'Deciphering', 'Translating', 'Piecing clues',
  'Following the thread', 'Chasing ideas', 'Catching up', 'Lining up thoughts', 'Setting the table', 'Laying groundwork', 'Laying it out', 'Roughing it in',
  'Drafting', 'Outlining', 'Penciling in', 'Inking', 'Coloring in', 'Shading', 'Framing', 'Composing',
  'Arranging notes', 'Humming along', 'Whistling', 'Warming the engine', 'Gathering steam', 'Picking up speed', 'Rounding the corner', 'Nearly done',
  'Just a moment', 'One moment', 'Bear with me',
] as const

export const WORKING_WORD_INTERVAL_MS = 3500
const RECENT_WORDS_TO_AVOID = 8

// A random word that is not one of the last few shown. Pure (the random source is passed in) so it can be tested.
export function nextWorkingWordIndex(recent: readonly number[], random: () => number = Math.random): number {
  const avoid = new Set(recent.slice(-RECENT_WORDS_TO_AVOID))
  const choices: number[] = []
  for (let index = 0; index < WORKING_WORDS.length; index += 1) if (!avoid.has(index)) choices.push(index)
  return choices[Math.floor(random() * choices.length)]
}

// The glyph twinkles through these shapes and back (about 130 ms a frame); reduced motion shows one still star.
// Every glyph carries the text presentation selector (U+FE0E) and none has an emoji form that could replace it:
// U+2733 (eight spoked asterisk) and U+2734 render as colour emoji (a green square) on Apple systems, so they are not used.
const TEXT = '\uFE0E'
export const WORKING_GLYPH_FRAMES = ['·', '✢', '✶', '✻', '✽', '✻', '✶', '✢'].map((glyph) => glyph + TEXT) as readonly string[]
export const WORKING_GLYPH_STATIC = '✽' + TEXT
export const WORKING_GLYPH_INTERVAL_MS = 130
export const WORKING_ACCESSIBLE_LABEL = 'Working…'

// Statuses that only say "busy" carry no information, so a playful word replaces them.
const GENERIC_STATUSES = new Set(['', 'working', 'working…', 'working...', 'running', 'thinking'])

// What to show: a real status from the server (a tool running, "Waiting for background work…", a step name) wins over
// the playful word. Pure, so it can be tested without rendering.
export function resolveWorkingLabel(realStatus: string | undefined, playfulWord: string): { text: string; real: boolean } {
  const real = (realStatus ?? '').trim()
  if (real && !GENERIC_STATUSES.has(real.toLowerCase())) return { text: real, real: true }
  return { text: `${playfulWord}…`, real: false }
}

// 17s, then 1m 05s, then 1h 02m.
export function formatElapsed(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000))
  if (total < 60) return `${total}s`
  const minutes = Math.floor(total / 60)
  if (minutes < 60) return `${minutes}m ${String(total % 60).padStart(2, '0')}s`
  return `${Math.floor(minutes / 60)}h ${String(minutes % 60).padStart(2, '0')}m`
}

// Past-tense words for the line that closes a finished turn ("Brewed for 1m 12s"). Plain and friendly; none reads as a status.
export const TURN_DONE_WORDS = ['Brewed', 'Baked', 'Crafted', 'Polished', 'Worked', 'Cooked', 'Stitched', 'Wove', 'Sketched', 'Tinkered', 'Gathered', 'Pondered', 'Mulled', 'Shaped', 'Assembled', 'Sorted', 'Sifted', 'Mapped', 'Tuned', 'Folded', 'Whittled', 'Simmered', 'Steeped', 'Kneaded', 'Drafted', 'Pieced it together', 'Figured it out', 'Thought it through', 'Put it together', 'Worked it out'] as const

// The same turn always gets the same word.
export function turnDoneWord(key: string): string {
  let hash = 0
  for (let index = 0; index < key.length; index += 1) hash = (hash * 31 + key.charCodeAt(index)) >>> 0
  return TURN_DONE_WORDS[hash % TURN_DONE_WORDS.length]
}
