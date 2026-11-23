package musecli

func nativeHandoffInstructions(human, instructions string) string {
	return "[AgentWorks handoff: This native session moved between computers. Earlier workspace instructions are superseded. Follow these current destination instructions:\n" + instructions + "\nEnd current destination instructions.]\n\n" + human
}
