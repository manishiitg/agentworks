/** A request to focus the Files search box (⌘P by name, ⌘⇧F for text), from the code pane's shortcuts. */
export type FileSearchMode = 'name' | 'text'
export const FILE_SEARCH_EVENT = 'agentworks:focus-file-search'
// The Files tab may still be mounting when the key is pressed; it reads this once it shows.
let pendingFileSearch: { mode: FileSearchMode; at: number } | null = null
export function requestFileSearch(mode: FileSearchMode) {
  pendingFileSearch = { mode, at: Date.now() }
  window.dispatchEvent(new CustomEvent<FileSearchMode>(FILE_SEARCH_EVENT, { detail: mode }))
}
/** The recent request, if any; a key press only counts for the tab it opened, not a later visit. */
export function pendingFileSearchMode(): FileSearchMode | null {
  return pendingFileSearch && Date.now() - pendingFileSearch.at < 2000 ? pendingFileSearch.mode : null
}
export function clearPendingFileSearch() { pendingFileSearch = null }
