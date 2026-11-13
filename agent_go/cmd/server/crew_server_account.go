package server

// The AI account a person's agent uses on the server (owner, 13 Nov, PLAT-856-564: "for both.. we need both options
// 1. use your own 2. or if something is already provided by company"). Run online and Backup offer the same two:
// the person's own signed-in account, or an account the company provides (the server's account or one shared with
// them). One saved choice per person, Crew and AI program, kept on the server next to the person's Backup rows:
//   - Run online uses the saved choice; with none and more than one account it asks, and saves the answer;
//   - Backup uses the saved choice, else the person's own account, else the server's usual pick (the company's).
// A saved choice that is no longer offered to the person is ignored; access is checked again at every turn.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

const (
	crewServerAccountOwn     = "own"
	crewServerAccountCompany = "company"
)

func crewServerAccountPath(project, person string) (string, error) {
	dir, err := crewStandbyPersonDir(project, person)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "server-account.json"), nil
}

// readCrewServerAccounts is the person's saved choice per AI program in this Crew.
func readCrewServerAccounts(project, person string) map[string]string {
	out := map[string]string{}
	path, err := crewServerAccountPath(project, person)
	if err != nil {
		return out
	}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func writeCrewServerAccount(project, person, provider, account string) error {
	path, err := crewServerAccountPath(project, person)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	saved := readCrewServerAccounts(project, person)
	saved[provider] = account
	return fsutil.WriteJSONAtomic(path, saved, 0o600)
}

// crewServerAccountOptions are the accounts for provider this person may use for this Crew on this server: their own
// first, then the ones the company provides. Only usable, set-up accounts are offered.
func (api *StreamingAPI) crewServerAccountOptions(ctx context.Context, claims *UserClaims, roots crewroot.Roots, provider string) ([]crewAgentPublishAccount, error) {
	views, err := api.listProviderAccountViews(ctx, claims.UserID, false, providerAccountScope{Principal: claims.UserID, WorkspacePath: roots.Virtual, Product: "crew"})
	if err != nil {
		return nil, err
	}
	out := []crewAgentPublishAccount{}
	for _, v := range views {
		if v.Provider != provider || !v.Usable || v.Configured != nil && !*v.Configured {
			continue
		}
		account := crewAgentPublishAccount{ID: v.ID, Name: v.DisplayName, Identity: v.Identity}
		switch v.Relation {
		case "own":
			account.Kind = crewServerAccountOwn
		case "server", "shared_with_you", "shared_with_crew":
			account.Kind = crewServerAccountCompany
			if v.Relation == "server" {
				account.Name = "the company's " + providerAccountProductName(provider) + " account"
			}
		default:
			continue
		}
		out = append(out, account)
	}
	slices.SortStableFunc(out, func(a, b crewAgentPublishAccount) int {
		return strings.Compare(crewServerAccountRank(a.Kind), crewServerAccountRank(b.Kind))
	})
	return out, nil
}

func crewServerAccountRank(kind string) string {
	if kind == crewServerAccountOwn {
		return "0"
	}
	return "1"
}

// crewServerAccountLabel names an account for the person: its name and whose it is.
func crewServerAccountLabel(a crewAgentPublishAccount) string {
	label := a.Name
	if a.Kind == crewServerAccountOwn {
		label = firstNonEmptyTrimmed(a.Identity, a.Name) + " (your own)"
	} else if a.Name != "" && !strings.HasPrefix(a.Name, "the company's") {
		label += " (provided by the company)"
	}
	return label
}

// crewServerAccountChoiceLine lists the offered accounts with their ids, for the agent to ask the person.
func crewServerAccountChoiceLine(options []crewAgentPublishAccount) string {
	parts := make([]string, 0, len(options))
	for _, o := range options {
		parts = append(parts, fmt.Sprintf("%s [account_id %s]", crewServerAccountLabel(o), o.ID))
	}
	return strings.Join(parts, "; ")
}

// crewServerAccountPick is the saved choice when it is still offered, else "".
func crewServerAccountPick(project, person, provider string, options []crewAgentPublishAccount) string {
	saved := readCrewServerAccounts(project, person)[provider]
	for _, o := range options {
		if saved != "" && o.ID == saved {
			return saved
		}
	}
	return ""
}

// crewBackupServerAccount is the account an Always online turn of person's agent on provider uses (owner, 13 Nov:
// "automatic if we have same account else pick what we have done on server"): the server account signed in as the same
// login the agent uses on the Mac (macLogin, the login the program reports, usually an email; a key has none), else
// the saved choice, else their own account, else "" (the server's usual pick, the company's account).
func (api *StreamingAPI) crewBackupServerAccount(ctx context.Context, claims *UserClaims, roots crewroot.Roots, provider, macLogin string) (string, string) {
	options, err := api.crewServerAccountOptions(ctx, claims, roots, provider)
	if err != nil || len(options) == 0 {
		return "", ""
	}
	if login := strings.TrimSpace(macLogin); login == crewMacCompanyLogin {
		// The Mac agent runs on this server's company account (through the server's proxy): the same account here.
		for _, o := range options {
			if o.ID == "global:"+provider {
				return o.ID, "same_login"
			}
		}
	} else if strings.Contains(login, "@") {
		// A login is matched only by its email: a sign-in method such as "ChatGPT" says nothing about whose it is.
		for _, o := range options {
			if strings.EqualFold(strings.TrimSpace(o.Identity), login) {
				return o.ID, "same_login"
			}
		}
	}
	if picked := crewServerAccountPick(roots.ID, claims.UserID, provider, options); picked != "" {
		return picked, "chosen"
	}
	if options[0].Kind == crewServerAccountOwn {
		return options[0].ID, crewServerAccountOwn
	}
	return "", ""
}

// receiveCrewServerAccount answers the Mac's "which account on the server" (action server_account): the offered
// accounts and the current choice; with account_id it saves that choice.
func (api *StreamingAPI) receiveCrewServerAccount(ctx context.Context, claims *UserClaims, id string, in crewSyncRequest) (crewSyncReply, error) {
	roots, binding, err := api.crewTransferBinding(ctx, claims, id, in.PairID)
	if err != nil {
		return crewSyncReply{}, err
	}
	out := crewSyncReply{ProjectID: id, Binding: binding}
	provider := strings.TrimSpace(in.Provider)
	if provider == "" {
		return out, fmt.Errorf("say which AI program the account is for")
	}
	options, err := api.crewServerAccountOptions(ctx, claims, roots, provider)
	if err != nil {
		return out, err
	}
	preview := &crewAgentPublishPreview{Accounts: options}
	if want := strings.TrimSpace(in.AccountID); want != "" {
		if !slices.ContainsFunc(options, func(o crewAgentPublishAccount) bool { return o.ID == want }) {
			return out, fmt.Errorf("that account is not one you can use on this server; choose one of: %s", crewServerAccountChoiceLine(options))
		}
		if err = writeCrewServerAccount(roots.ID, claims.UserID, provider, want); err != nil {
			return out, err
		}
		logCrewServerAccount(roots.ID, claims.UserID, provider, want, "chosen")
	}
	preview.Selection.AccountID = crewServerAccountPick(roots.ID, claims.UserID, provider, options)
	preview.Ready = preview.Selection.AccountID != ""
	out.Publish = preview
	return out, nil
}

func logCrewServerAccount(project, person, provider, account, how string) {
	log.Printf("[CREW_SERVER_ACCOUNT] project=%s person=%s program=%s account=%s how=%s", project, logUsernameForUserID(person), provider, account, how)
}

// crewServerAccountChatAction is manage_crew_sync action=server_account on the Mac: which account this agent's AI
// program uses on the server, for Run online and Backup, and with account_id, saving the person's choice.
func (api *StreamingAPI) crewServerAccountChatAction(ctx context.Context, claims *UserClaims, project, sessionID string, args map[string]any) (string, error) {
	binding, err := crewroot.ReadSync(project)
	if err != nil || binding == nil || binding.Side != "local" {
		return "", fmt.Errorf("turn on Use from phone and web first; the server account is chosen on that server")
	}
	connection, err := desktopConnectionFor(claims.UserID, binding.ConnectionID)
	if err != nil {
		return "", err
	}
	roots, err := api.crewSyncRoots(ctx, claims, project)
	if err != nil {
		return "", err
	}
	rows, err := api.macAgentRows(ctx, claims, project)
	if err != nil {
		return "", err
	}
	tab, err := api.crewRunOnlineSource(ctx, claims, project, sessionID, strings.TrimSpace(externalArg(args, "agent")), rows)
	if err != nil {
		return "", err
	}
	record, _, err := api.crewRunOnlineRecord(ctx, claims, roots, tab)
	if err != nil {
		return "", err
	}
	provider := strings.TrimSpace(record.Provider)
	if provider == "" {
		return "", fmt.Errorf("this agent has no AI program chosen yet")
	}
	remote, err := desktopCrewSyncCall(ctx, claims.UserID, &connection, binding.PeerProjectID, crewSyncRequest{Action: "server_account", PairID: binding.PairID, Provider: provider, AccountID: strings.TrimSpace(externalArg(args, "account_id"))})
	if err != nil {
		return "", err
	}
	if remote.Publish == nil {
		return "", fmt.Errorf("update the server to choose an account there")
	}
	program := providerAccountProductName(provider)
	options := remote.Publish.Accounts
	if len(options) == 0 {
		return "There is no " + program + " account you can use on the server yet. Sign in once there in Providers → Add my account, or ask the admin to give you the company's account. Until then Backup waits for this Mac and Run online cannot start.", nil
	}
	current := "none chosen yet: Backup uses your own account when you have one there, else the company's, and Run online asks"
	for _, o := range options {
		if o.ID == remote.Publish.Selection.AccountID {
			current = crewServerAccountLabel(o)
		}
	}
	return fmt.Sprintf("%s on the server, for Run online and Backup: %s. Accounts you can use: %s. To change it, ask the person which one and call again with account_id.", program, current, crewServerAccountChoiceLine(options)), nil
}

// crewServerAccountKind says whose account a server chat ran on, in plain words for the Team tab: "your account" or
// "company account" ("" when unknown). A "global:" account is the server's own, the company's.
func (api *StreamingAPI) crewServerAccountKind(ctx context.Context, claims *UserClaims, roots crewroot.Roots, provider, account string) string {
	account = strings.TrimSpace(account)
	if account == "" || strings.HasPrefix(account, "global:") {
		// No account recorded means the server's usual pick, its own (the company's) account.
		return crewServerAccountCompany
	}
	options, err := api.crewServerAccountOptions(ctx, claims, roots, provider)
	if err != nil {
		return ""
	}
	for _, o := range options {
		if o.ID == account {
			return o.Kind
		}
	}
	return ""
}

// crewMacCompanyLogin is what the Mac reports for an agent running on its server's company account
// (checkProviderAccountStatus).
const crewMacCompanyLogin = "company account"

var crewMacLoginCache sync.Map // account -> crewMacLoginSeen

type crewMacLoginSeen struct {
	login string
	at    time.Time
}

// crewStandbyMacLogin (Mac) is the login the agent's AI account reports, sent with its Always online copy. The program's
// status command runs at most every 30 minutes per account; "" when it is unknown.
func (api *StreamingAPI) crewStandbyMacLogin(ctx context.Context, record ProductConversationRecord) string {
	provider := strings.TrimSpace(record.Provider)
	if provider == "" {
		return ""
	}
	account := firstNonEmptyTrimmed(record.ConnectionID, "global:"+provider)
	if seen, ok := crewMacLoginCache.Load(account); ok && time.Since(seen.(crewMacLoginSeen).at) < 30*time.Minute {
		return seen.(crewMacLoginSeen).login
	}
	target, err := api.resolveProviderAccountTarget(ctx, account, provider)
	if err != nil {
		return ""
	}
	login := strings.TrimSpace(checkProviderAccountStatus(ctx, target, false).Identity)
	crewMacLoginCache.Store(account, crewMacLoginSeen{login: login, at: time.Now()})
	return login
}

// crewMacLoginKind says, for logs, what kind of login the Mac reported for Always online without writing the login
// itself: "email", "company", "other" (a sign-in method such as "ChatGPT" or an API key) or "none" (not sent).
func crewMacLoginKind(login string) string {
	switch login = strings.TrimSpace(login); {
	case login == "":
		return "none"
	case login == crewMacCompanyLogin:
		return "company"
	case strings.Contains(login, "@"):
		return "email"
	}
	return "other"
}
