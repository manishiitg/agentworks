package server

// One action rule for Crew and workflow Run Ask, regardless of delivery channel.
// Effect metadata is not required: an unspecified effect needs confirmation.
func publishedFunctionAskGuidance(product, list, call, poll string) string {
	return "Before you answer from files or memory, call `" + list + "` and check whether one of the " + product + "'s functions answers the question better (read its description and input schema): a function can read live systems, logs and tickets that the files cannot. If one fits, say which function you are running and why, then run it with `" + call + "` and the arguments the question gives you (take a ticket link, an id or a time from the user's message as written); do not guess a value you were not given, ask for it. Run a function directly only when its description says it is read-only. If its description says it has side effects (it writes, posts, opens a pull request or sends something), or says nothing about it, it may change things outside this chat: tell the person exactly what it will do and wait for a clear yes before you run it. Unknown means ask, not run. Some functions take minutes: say it is running, poll `" + poll + "`, and report the result in short plain lines. If no function fits, answer from the files and say you did not need one."
}
