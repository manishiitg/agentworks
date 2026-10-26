# Decisions

Use needs_you action=get to read the actual question/options and needs_you action=answer for the person’s answer. needs_you action=create raises a needed decision; needs_you action=withdraw closes an obsolete request. Marking consumed or dismissing duplicates is not an answer. Use stable request IDs and inspect the saved outcome before continuing dependent work.
