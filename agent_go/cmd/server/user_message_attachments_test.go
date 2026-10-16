package server

import "testing"

func TestUserMessageAttachmentsFromTheFilesInContextLine(t *testing.T) {
	got := userMessageAttachments("what is this?\n\n📁 Files in context: Chats/Work/p/uploads/a.png, Chats/Work/p/uploads/b.pdf, Chats/Work/p/uploads/a.png")
	if len(got) != 2 || got[0]["kind"] != "image" || got[0]["name"] != "a.png" || got[1]["kind"] != "file" || got[1]["path"] != "Chats/Work/p/uploads/b.pdf" {
		t.Fatalf("attachments = %v", got)
	}
	if userMessageAttachments("no files here") != nil {
		t.Fatal("a message without a Files in context line has no attachments")
	}
}
