---
name: reading-book
description: Create illustrated reading material as a complete page-by-page book with navigation and printable pages in a SparkQuill activity.
---

# Reading book

Create illustrated reading material as a complete page-by-page book with navigation and printable pages in a SparkQuill activity.

Use this in Parent Mode for a reading book, storybook, picture book, chapter-style lesson or study material in book format. Match the child's known reading level, language, grade and interests. Use uploaded school material when the request concerns it. If no subject is stated or reasonably established by the conversation, ask one focused topic question; otherwise start writing. Respect any requested page count. For an unspecified short book, 6–10 pages is a useful starting point, adjusted to the amount of material and reading level.

## Write the whole book

Plan a cover, an inviting opening, a sequence of pages that develops the story or idea, and a satisfying ending or recap. Give each page a short title, a readable amount of prose, and a purposeful illustration or diagram when useful. A story has characters and a complete arc; an explanatory book builds understanding through concrete examples. Define new words in context. Do not turn every page into a quiz. Add a few optional discussion or comprehension questions at natural stopping points or at the end.

For internet pictures, prefer Google Images through `agent_browser`. Open a result's source page and download the actual image into the activity folder rather than embedding a search-results thumbnail or remote URL. Inspect the saved picture for relevance, clarity and suitability for the child, and retain its source and any required credit. Use `image_gen` for custom illustrations or inline SVG for diagrams when those fit better, keeping illustrations consistent across pages. Reference saved assets with relative filenames and descriptive alt text. Complete the actual prose and illustrations before delivery; a table of contents and a promise to generate later pages is unfinished.

## Build the reading experience

Use [assets/book.sq.html](assets/book.sq.html) as an optional working starting point. It demonstrates pagination, accessible controls, saved position and print layout using a short seed-growing book. Replace its prose, illustrations, title and language for the actual request; the example's topic and page count are not defaults for every child.

Write one self-contained `book.sq.html` inside `activities/<yyyy-mm-dd>-<slug>/`. Keep all pages in that file so the child can move between them without waiting for the tutor. Use semantic `<article>` elements for pages, a cover, visible page numbers, a `Page X of Y` indicator, and labelled Previous and Next buttons. Show one page at a time, disable Previous on the cover and Next on the last page, and support keyboard navigation without hijacking keys while the child is typing an answer. Use buttons and inline JavaScript: the activity renderer unwraps links and `<details>`.

Choose generous reading type, strong contrast, restrained decoration and a responsive single-page layout that fits the viewer. Avoid fixed-height clipping. Honour reduced-motion preferences if animating page changes. Move focus to the new page's heading and announce the page number politely. Put every page in the HTML from the start, including the last page.

Remember the current page with `SQ.saveGame('reading-progress', {page: index})` and restore with `SQ.loadGame('reading-progress', function(saved) { ... })`. Treat null or invalid saved data as the cover, clamp a valid index to the available pages, and allow a child to return to the beginning. Parent preview does not save progress. Call SQ only after the bridge is available; put initialization in DOMContentLoaded or a readyState check rather than calling it from an immediately executed script.

For print, show all pages in order, hide navigation, remove screen-only positioning and use page breaks between articles. Print styles must override the screen's hidden-page rule, including any `[hidden]` attributes. The reading experience should also expose all pages if JavaScript fails. Keep practice solutions and any parent-only teaching notes in `keys/<activity-slug>-KEY.md`, outside the child's folder.

## Verify and hand over

Finalize `book.sq.html` with `create_learning_activity` using `items: ["book.sq.html"]`. Inspect its report and verify the finished `book.html`, not just the authoring source. Check that every planned page exists, navigation reaches the last page and returns, controls behave at both boundaries, progress restores where supported, images load, and print exposes every page. Inspect at a narrow viewer width as well as desktop size. Tutor-reviewed questions use `.q` and `SQ.answer` as normal; local page turning needs no tutor call.

Call `open_activity` to show the completed book for the parent's preview and Give button. Describe the book and what it teaches in plain language; do not imply it is already on the child's screen. If a parent explicitly requests a PDF or EPUB, provide that requested export in addition to the reading activity, and verify its pagination rather than calling HTML a PDF.
