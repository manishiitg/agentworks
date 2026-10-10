package server

import (
	"encoding/json"
	"testing"
)

func TestAddDirectoryUserCreatesOnceAndIsSafeToRepeat(t *testing.T) {
	content := withMemoryUserDirectory(t, `{"users":[{"id":"u1","username":"alice","email":"alice@x.com","products":["code"]}]}`)
	dir, err := readUserDirectoryFile()
	if err != nil {
		t.Fatal(err)
	}
	rec, created, err := addDirectoryUser(dir, " User@Example.com ", "", "editor", []string{"code"})
	if err != nil || !created || rec.Email != "user@example.com" || rec.Username != "user@example.com" || len(rec.Products) != 1 {
		t.Fatalf("created %v rec %+v err %v", created, rec, err)
	}
	if err := saveUserDirectory(dir); err != nil {
		t.Fatal(err)
	}
	var saved userDirectoryFile
	if json.Unmarshal([]byte(*content), &saved) != nil || len(saved.Users) != 2 {
		t.Fatalf("saved %q", *content)
	}
	again, createdAgain, err := addDirectoryUser(dir, "user@example.com", "", "viewer", []string{"code"})
	if err != nil || createdAgain || again.ID != rec.ID {
		t.Fatalf("a repeat must return the existing account unchanged: created %v %+v %v", createdAgain, again, err)
	}
}

func TestAddDirectoryUserRefusesBadInput(t *testing.T) {
	withMemoryUserDirectory(t, `{"users":[]}`)
	dir, _ := readUserDirectoryFile()
	for name, args := range map[string][3]string{
		"no email":     {"", "", "editor"},
		"not an email": {"nobody", "", "editor"},
		"unknown role": {"a@b.com", "", "emperor"},
		"bad username": {"a@b.com", "bad name!", "editor"},
	} {
		if _, _, err := addDirectoryUser(dir, args[0], args[1], args[2], []string{"code"}); err == nil {
			t.Fatalf("%s must be refused", name)
		}
	}
	if len(dir.Users) != 0 {
		t.Fatalf("a refused add left a record: %+v", dir.Users)
	}
}
