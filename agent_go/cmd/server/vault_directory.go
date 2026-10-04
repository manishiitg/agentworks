package server

import "sort"

// Vault identities come from the same central directory as Users & access.
// Gateway users can have blank/stale emails because runtime binding needs only
// an ID. Never use those records to guess a person's email or sign-in identity.
type vaultDirectoryUser struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Username string `json:"username"`
}

func vaultDirectoryUsers(claims *UserClaims) ([]vaultDirectoryUser, error) {
	dir, err := readUserDirectoryFile()
	if err != nil {
		return nil, err
	}
	users := make([]vaultDirectoryUser, 0, len(dir.Users))
	for _, user := range dir.Users {
		if !user.Disabled {
			users = append(users, vaultDirectoryUser{ID: user.ID, Email: user.Email, Username: user.Username})
		}
	}
	if !IsMultiUserMode() && len(users) == 0 && claims != nil {
		users = append(users, vaultDirectoryUser{ID: claims.UserID, Email: claims.Email, Username: claims.Username})
	}
	sort.Slice(users, func(i, j int) bool {
		if users[i].Email != users[j].Email {
			return users[i].Email < users[j].Email
		}
		return users[i].ID < users[j].ID
	})
	return users, nil
}
