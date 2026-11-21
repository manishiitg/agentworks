package server

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/teamonline"
)

const teamOnlineMacButtonRefusal = "A button on this page asks your Mac’s agent. Point it at the Team agent first, so it works when your Mac is off."

// Explicit source-Crew agent addresses cannot become server authority by being
// included in HTML. Declared ordinary functions are installed independently in
// the Team Crew; their quoted names are rebound in the staged code only.
func teamOnlineBindDashboardFunctions(source triggerTarget, p *teamOnlinePackage) error {
	file, err := p.root.Open(teamonline.Descriptor)
	if err != nil {
		return err
	}
	item, err := teamonline.DecodeItem(file)
	file.Close()
	if err != nil {
		return err
	}
	for name, content := range item.Snapshot.Files {
		if name == "online.json" {
			continue
		}
		for _, address := range []string{"workspace:work:" + source.CrewID, source.CrewID + codeChatSideMarker} {
			if strings.Contains(content, address) {
				return fmt.Errorf("%s", teamOnlineMacButtonRefusal)
			}
		}
		for _, function := range item.Dependencies.Functions {
			replacement := teamOnlineFunctionName(source.CrewID, function)
			content = strings.ReplaceAll(content, "\""+function+"\"", "\""+replacement+"\"")
			content = strings.ReplaceAll(content, "'"+function+"'", "'"+replacement+"'")
		}
		item.Snapshot.Files[name] = content
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	file, err = p.root.OpenFile(teamonline.Descriptor, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(raw)
	ce := file.Close()
	if err != nil {
		return err
	}
	return ce
}
