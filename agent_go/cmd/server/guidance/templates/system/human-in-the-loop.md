# Questions in conversations

An attended Crew conversation can ask its person a question using the program's native question tool or the offered clarification tool. Shared-Crew Ask uses its existing scoped question/reply path. Keep questions concise and within the current conversation; a child or another person must not answer as its owner.

Background work and messages started by another agent use the existing unattended handling. Do not invent a new waiting state, send a question to another audience or change the current tool set simply because the turn was automatic.

Saved Python workflows take declared inputs before they start; they do not pause on a plan human-input step. If a required value is missing, ask for it in the Crew conversation before starting the requested work. A running program is not corrected by sending a message to an old plan step. Inspect its real evidence; stop the returned execution only when requested, then edit and run through the current supported entry.
